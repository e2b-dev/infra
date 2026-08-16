package kubernetesserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const maxEnvdErrorBody = 4 << 10

var (
	errResourceIdentityConflict = errors.New("resource identity conflict")
	errPodUnschedulable         = errors.New("sandbox Pod is unschedulable")
)

type podWaiter func(context.Context, string) (*corev1.Pod, error)

// Server implements E2B's SandboxService with Kubernetes Pods as its only
// lifecycle primitive. The selected Kata RuntimeClass is an implementation
// detail hidden behind this interface.
type Server struct {
	orchestrator.UnimplementedSandboxServiceServer

	client       kubernetes.Interface
	config       Config
	clientID     string
	httpClient   *http.Client
	waitForReady podWaiter
}

func New(client kubernetes.Interface, config Config) (*Server, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	server := &Server{
		client:     client,
		config:     config,
		clientID:   config.NodeID,
		httpClient: &http.Client{Timeout: config.CreateTimeout},
	}
	server.waitForReady = server.waitForPodReady
	return server, nil
}

func (s *Server) ClientID() string {
	return s.clientID
}

func (s *Server) Create(ctx context.Context, req *orchestrator.SandboxCreateRequest) (_ *orchestrator.SandboxCreateResponse, createErr error) {
	if err := validateCreateRequest(req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	sandboxConfig := req.GetSandbox()
	runtimeClass, err := s.config.runtimeClass(sandboxConfig.GetMetadata())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	image, err := s.config.sandboxImage(sandboxConfig.GetTemplateId(), sandboxConfig.GetBuildId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	identity, err := requestIdentity(req, runtimeClass, image)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pod, err := buildPod(s.config, req, runtimeClass, image, identity)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	secret, err := buildSecret(s.config, req, runtimeClass, identity)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	initPayload, err := buildInitPayload(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	networkPolicy, err := buildNetworkPolicy(s.config, sandboxConfig, runtimeClass, identity)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	createCtx, cancel := context.WithTimeout(ctx, s.config.CreateTimeout)
	defer cancel()

	var createdSecret, createdNetworkPolicy, createdPod bool
	defer func() {
		if createErr == nil || (!createdSecret && !createdNetworkPolicy && !createdPod) {
			return
		}
		if cleanupErr := s.cleanupNewResources(createdSecret, createdNetworkPolicy, createdPod, sandboxConfig.GetSandboxId()); cleanupErr != nil {
			createErr = errors.Join(createErr, fmt.Errorf("clean up failed sandbox creation: %w", cleanupErr))
		}
	}()

	createdSecret, err = s.ensureSecret(createCtx, secret, identity)
	if err != nil {
		return nil, grpcKubernetesError("create sandbox Secret", err)
	}
	createdNetworkPolicy, err = s.ensureNetworkPolicy(createCtx, networkPolicy, runtimeClass)
	if err != nil {
		return nil, grpcKubernetesError("create sandbox NetworkPolicy", err)
	}
	createdPod, err = s.ensurePod(createCtx, pod, identity)
	if err != nil {
		return nil, grpcKubernetesError("create sandbox Pod", err)
	}

	readyPod, err := s.waitForReady(createCtx, pod.Name)
	if err != nil {
		return nil, grpcKubernetesError("wait for sandbox Pod", err)
	}
	initPayload, err = initPayloadForPod(initPayload, readyPod)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "prepare sandbox envd init: %v", err)
	}
	if err := s.initializeEnvd(createCtx, readyPod, initPayload); err != nil {
		return nil, status.Errorf(codes.Unavailable, "initialize sandbox envd: %v", err)
	}

	return &orchestrator.SandboxCreateResponse{ClientId: s.clientID}, nil
}

func validateCreateRequest(req *orchestrator.SandboxCreateRequest) error {
	if req == nil || req.GetSandbox() == nil {
		return fmt.Errorf("sandbox config is required")
	}
	cfg := req.GetSandbox()
	if cfg.GetSandboxId() == "" {
		return fmt.Errorf("sandbox_id is required")
	}
	if cfg.GetTeamId() == "" {
		return fmt.Errorf("team_id is required")
	}
	if cfg.GetTemplateId() == "" || cfg.GetBuildId() == "" {
		return fmt.Errorf("template_id and build_id are required")
	}
	if cfg.GetVcpu() <= 0 || cfg.GetRamMb() <= 0 {
		return fmt.Errorf("vcpu and ram_mb must both be greater than zero")
	}
	if cfg.GetSnapshot() {
		return fmt.Errorf("snapshot resume is not supported by the Kubernetes backend")
	}
	if cfg.GetAutoPause() || cfg.GetAutoPauseFilesystemOnly() {
		return fmt.Errorf("pause and auto-resume are not supported by the Kubernetes backend")
	}
	if autoResume := cfg.GetAutoResume(); autoResume != nil && autoResume.GetPolicy() != "" && autoResume.GetPolicy() != "off" {
		return fmt.Errorf("pause and auto-resume are not supported by the Kubernetes backend")
	}
	if len(cfg.GetVolumeMounts()) > 0 {
		return fmt.Errorf("volume mounts are not supported by the Kubernetes backend")
	}
	if cfg.GetIam() != nil {
		return fmt.Errorf("sandbox IAM is not supported by the Kubernetes backend")
	}
	if startTime := req.GetStartTime(); startTime != nil && !startTime.IsValid() {
		return fmt.Errorf("start_time is invalid")
	}
	if endTime := req.GetEndTime(); endTime != nil && !endTime.IsValid() {
		return fmt.Errorf("end_time is invalid")
	}
	return nil
}

func (s *Server) ensureSecret(ctx context.Context, desired *corev1.Secret, identity string) (bool, error) {
	_, err := s.client.CoreV1().Secrets(s.config.Namespace).Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return true, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	existing, err := s.client.CoreV1().Secrets(s.config.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if existing.Annotations[identityAnnotation] != identity {
		return false, fmt.Errorf("%w: resource %q already exists for a different sandbox request", errResourceIdentityConflict, desired.Name)
	}
	return false, nil
}

func (s *Server) ensureNetworkPolicy(ctx context.Context, desired *networkingv1.NetworkPolicy, runtimeClass string) (bool, error) {
	_, err := s.client.NetworkingV1().NetworkPolicies(s.config.Namespace).Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return true, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	existing, err := s.client.NetworkingV1().NetworkPolicies(s.config.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if existing.Labels[sandboxIDLabel] != desired.Labels[sandboxIDLabel] || existing.Labels[runtimeClassLabel] != runtimeClass || existing.Annotations[identityAnnotation] != desired.Annotations[identityAnnotation] {
		return false, fmt.Errorf("%w: resource %q already exists for a different sandbox request", errResourceIdentityConflict, desired.Name)
	}
	return false, nil
}

func (s *Server) ensurePod(ctx context.Context, desired *corev1.Pod, identity string) (bool, error) {
	_, err := s.client.CoreV1().Pods(s.config.Namespace).Create(ctx, desired, metav1.CreateOptions{})
	if err == nil {
		return true, nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	existing, err := s.client.CoreV1().Pods(s.config.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if existing.Annotations[identityAnnotation] != identity {
		return false, fmt.Errorf("%w: resource %q already exists for a different sandbox request", errResourceIdentityConflict, desired.Name)
	}
	return false, nil
}

func (s *Server) cleanupNewResources(secret, networkPolicy, pod bool, sandboxID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), s.config.DeleteTimeout)
	defer cancel()
	var errs []error
	if pod {
		if err := s.deletePodAndWait(cleanupCtx, podName(sandboxID)); err != nil {
			errs = append(errs, fmt.Errorf("delete Pod: %w", err))
			return errors.Join(errs...)
		}
	} else if _, err := s.client.CoreV1().Pods(s.config.Namespace).Get(cleanupCtx, podName(sandboxID), metav1.GetOptions{}); err == nil {
		// Supporting resources created around an already-existing Pod are part
		// of that sandbox's fail-closed state. Keep them for an idempotent retry.
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check Pod before cleanup: %w", err)
	}
	if networkPolicy {
		if err := s.client.NetworkingV1().NetworkPolicies(s.config.Namespace).Delete(cleanupCtx, networkPolicyName(sandboxID), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete NetworkPolicy: %w", err))
		}
	}
	if secret {
		if err := s.client.CoreV1().Secrets(s.config.Namespace).Delete(cleanupCtx, secretName(sandboxID), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete Secret: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) deletePodAndWait(ctx context.Context, name string) error {
	err := s.client.CoreV1().Pods(s.config.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	return wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		_, err := s.client.CoreV1().Pods(s.config.Namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			return true, nil
		case err != nil:
			return false, err
		default:
			return false, nil
		}
	})
}

func (s *Server) waitForPodReady(ctx context.Context, name string) (*corev1.Pod, error) {
	var ready *corev1.Pod
	err := wait.PollUntilContextCancel(ctx, 250*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		pod, err := s.client.CoreV1().Pods(s.config.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return false, fmt.Errorf("pod terminated in phase %s: %s", pod.Status.Phase, pod.Status.Message)
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse && condition.Reason == corev1.PodReasonUnschedulable {
				return false, fmt.Errorf("%w: %s", errPodUnschedulable, condition.Message)
			}
		}
		if pod.Status.PodIP == "" || !isPodReady(pod) {
			return false, nil
		}
		ready = pod
		return true, nil
	})
	return ready, err
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func (s *Server) initializeEnvd(ctx context.Context, pod *corev1.Pod, payload []byte) error {
	if pod == nil || pod.Status.PodIP == "" {
		return fmt.Errorf("ready Pod has no Pod IP")
	}
	target := url.URL{Scheme: "http", Host: net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(int(s.config.EnvdPort))), Path: "/init"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	response, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxEnvdErrorBody))
		return fmt.Errorf("envd returned %s: %s", response.Status, string(body))
	}
	return nil
}

func (s *Server) Update(ctx context.Context, req *orchestrator.SandboxUpdateRequest) (*emptypb.Empty, error) {
	if req == nil || req.GetSandboxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id is required")
	}
	if endTime := req.GetEndTime(); endTime != nil && !endTime.IsValid() {
		return nil, status.Error(codes.InvalidArgument, "end_time is invalid")
	}
	if egress := req.GetEgress(); egress != nil {
		probe := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{Egress: egress}}
		if _, _, err := buildEgressRules(probe); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	podName := podName(req.GetSandboxId())
	if req.GetEndTime() != nil {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			pod, err := s.client.CoreV1().Pods(s.config.Namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if pod.Annotations == nil {
				pod.Annotations = make(map[string]string)
			}
			pod.Annotations[endTimeAnnotation] = req.GetEndTime().AsTime().UTC().Format(time.RFC3339Nano)
			_, err = s.client.CoreV1().Pods(s.config.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
			return err
		})
		if err != nil {
			return nil, grpcKubernetesError("update sandbox end time", err)
		}
	}

	if req.GetEgress() != nil {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			pod, err := s.client.CoreV1().Pods(s.config.Namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return err
			}
			policy, err := s.client.NetworkingV1().NetworkPolicies(s.config.Namespace).Get(ctx, networkPolicyName(req.GetSandboxId()), metav1.GetOptions{})
			if err != nil {
				return err
			}
			cfg := &orchestrator.SandboxConfig{
				SandboxId: req.GetSandboxId(),
				Network:   &orchestrator.SandboxNetworkConfig{Egress: req.GetEgress()},
			}
			desired, err := buildNetworkPolicy(s.config, cfg, pod.Labels[runtimeClassLabel], pod.Annotations[identityAnnotation])
			if err != nil {
				return err
			}
			policy.Spec = desired.Spec
			_, err = s.client.NetworkingV1().NetworkPolicies(s.config.Namespace).Update(ctx, policy, metav1.UpdateOptions{})
			return err
		})
		if err != nil {
			return nil, grpcKubernetesError("update sandbox egress", err)
		}
	}

	if req.GetEndTime() == nil && req.GetEgress() == nil {
		if _, err := s.client.CoreV1().Pods(s.config.Namespace).Get(ctx, podName, metav1.GetOptions{}); err != nil {
			return nil, grpcKubernetesError("find sandbox", err)
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) List(ctx context.Context, _ *emptypb.Empty) (*orchestrator.SandboxListResponse, error) {
	pods, err := s.client.CoreV1().Pods(s.config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: managedSandboxSelector()})
	if err != nil {
		return nil, grpcKubernetesError("list sandbox Pods", err)
	}

	sandboxes := make([]*orchestrator.RunningSandbox, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}
		sandboxID := pod.Labels[sandboxIDLabel]
		if sandboxID == "" {
			continue
		}
		sandbox := &orchestrator.RunningSandbox{
			ClientId:    s.clientID,
			SandboxId:   sandboxID,
			TeamId:      pod.Annotations[teamIDAnnotation],
			ExecutionId: pod.Annotations[executionIDAnnotation],
			Vcpu:        parseInt64(pod.Annotations[vcpuAnnotation]),
			RamMb:       parseInt64(pod.Annotations[ramMiBAnnotation]),
		}
		sandbox.StartTime = parseTimestamp(pod.Annotations[startTimeAnnotation])
		if sandbox.StartTime == nil && !pod.CreationTimestamp.IsZero() {
			sandbox.StartTime = timestamppb.New(pod.CreationTimestamp.Time)
		}
		sandbox.EndTime = parseTimestamp(pod.Annotations[endTimeAnnotation])
		sandboxes = append(sandboxes, sandbox)
	}
	sort.Slice(sandboxes, func(i, j int) bool { return sandboxes[i].GetSandboxId() < sandboxes[j].GetSandboxId() })
	return &orchestrator.SandboxListResponse{Sandboxes: sandboxes}, nil
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func parseTimestamp(value string) *timestamppb.Timestamp {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return timestamppb.New(parsed)
}

func (s *Server) Delete(ctx context.Context, req *orchestrator.SandboxDeleteRequest) (*emptypb.Empty, error) {
	if req == nil || req.GetSandboxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id is required")
	}
	deleteCtx, cancel := context.WithTimeout(ctx, s.config.DeleteTimeout)
	defer cancel()

	deleteOptions := metav1.DeleteOptions{}
	var errs []error
	if err := s.deletePodAndWait(deleteCtx, podName(req.GetSandboxId())); err != nil {
		return nil, grpcKubernetesError("delete sandbox Pod", err)
	}
	if err := s.client.NetworkingV1().NetworkPolicies(s.config.Namespace).Delete(deleteCtx, networkPolicyName(req.GetSandboxId()), deleteOptions); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete NetworkPolicy: %w", err))
	}
	if err := s.client.CoreV1().Secrets(s.config.Namespace).Delete(deleteCtx, secretName(req.GetSandboxId()), deleteOptions); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Errorf("delete Secret: %w", err))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, grpcKubernetesError("delete sandbox resources", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *Server) Pause(context.Context, *orchestrator.SandboxPauseRequest) (*orchestrator.SandboxPauseResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Kata memory/filesystem pause is not supported; delete and recreate the sandbox")
}

func (s *Server) Checkpoint(context.Context, *orchestrator.SandboxCheckpointRequest) (*orchestrator.SandboxCheckpointResponse, error) {
	return nil, status.Error(codes.Unimplemented, "Kata checkpoint is not supported by the Kubernetes backend")
}

func grpcKubernetesError(operation string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, errResourceIdentityConflict):
		return status.Errorf(codes.AlreadyExists, "%s: %v", operation, err)
	case errors.Is(err, errPodUnschedulable):
		return status.Errorf(codes.ResourceExhausted, "%s: %v", operation, err)
	case apierrors.IsNotFound(err):
		return status.Errorf(codes.NotFound, "%s: %v", operation, err)
	case apierrors.IsAlreadyExists(err):
		return status.Errorf(codes.AlreadyExists, "%s: %v", operation, err)
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return status.Errorf(codes.PermissionDenied, "%s: %v", operation, err)
	case errors.Is(err, context.Canceled):
		return status.Errorf(codes.Canceled, "%s: %v", operation, err)
	case errors.Is(err, context.DeadlineExceeded):
		return status.Errorf(codes.DeadlineExceeded, "%s: %v", operation, err)
	default:
		return status.Errorf(codes.Internal, "%s: %v", operation, err)
	}
}

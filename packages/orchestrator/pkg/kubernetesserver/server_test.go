package kubernetesserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func stringPointer(value string) *string { return &value }

func testCreateRequest(runtimeClass string) *orchestrator.SandboxCreateRequest {
	metadata := map[string]string{}
	if runtimeClass != "" {
		metadata[RuntimeClassMetadataKey] = runtimeClass
	}
	allowInternet := true
	return &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			SandboxId:           "sbx-0123456789",
			TeamId:              "team-1",
			ExecutionId:         "execution-1",
			TemplateId:          "python",
			BaseTemplateId:      "python",
			BuildId:             "build-42",
			Vcpu:                2,
			RamMb:               1024,
			TotalDiskSizeMb:     4096,
			HugePages:           true,
			FirecrackerVersion:  "1.12.0",
			Metadata:            metadata,
			EnvVars:             map[string]string{"HELLO": "world"},
			EnvdAccessToken:     stringPointer("envd-secret"),
			AllowInternetAccess: &allowInternet,
			Network: &orchestrator.SandboxNetworkConfig{Ingress: &orchestrator.SandboxNetworkIngressConfig{
				TrafficAccessToken: stringPointer("traffic-secret"),
				MaskRequestHost:    stringPointer("service-${PORT}.internal"),
			}},
		},
		StartTime: timestamppb.New(time.Unix(1_700_000_000, 0)),
		EndTime:   timestamppb.New(time.Unix(1_700_003_600, 0)),
	}
}

func newEnvdTestServer(t *testing.T, statusCode int, calls *atomic.Int32) (*httptest.Server, int32) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/init", request.URL.Path)
		var payload envdInitPayload
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		assert.Equal(t, "world", payload.EnvVars["HELLO"])
		writer.WriteHeader(statusCode)
	}))
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	_, portText, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)
	port, err := strconv.ParseInt(portText, 10, 32)
	require.NoError(t, err)
	return server, int32(port)
}

func markReadyWaiter(client *fake.Clientset, namespace, podIP string) podWaiter {
	return func(ctx context.Context, name string) (*corev1.Pod, error) {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		pod.UID = types.UID("pod-lifecycle-1")
		pod.Status = corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: podIP,
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			}},
		}
		if _, err := client.CoreV1().Pods(namespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
			return nil, err
		}
		return pod, nil
	}
}

func TestCreateSupportsBothKataRuntimeClasses(t *testing.T) {
	for _, runtimeClass := range []string{RuntimeClassCLH, RuntimeClassQEMU} {
		t.Run(runtimeClass, func(t *testing.T) {
			var calls atomic.Int32
			envd, port := newEnvdTestServer(t, http.StatusNoContent, &calls)
			defer envd.Close()
			parsed, err := url.Parse(envd.URL)
			require.NoError(t, err)
			host, _, err := net.SplitHostPort(parsed.Host)
			require.NoError(t, err)

			cfg := testConfig(port)
			client := fake.NewSimpleClientset()
			server, err := New(client, cfg)
			require.NoError(t, err)
			server.waitForReady = markReadyWaiter(client, cfg.Namespace, host)

			request := testCreateRequest(runtimeClass)
			response, err := server.Create(t.Context(), request)
			require.NoError(t, err)
			assert.Equal(t, server.ClientID(), response.GetClientId())
			assert.Equal(t, cfg.NodeID, response.GetClientId())
			assert.EqualValues(t, 1, calls.Load())

			pod, err := client.CoreV1().Pods(cfg.Namespace).Get(t.Context(), podName(request.GetSandbox().GetSandboxId()), metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, pod.Spec.RuntimeClassName)
			assert.Equal(t, runtimeClass, *pod.Spec.RuntimeClassName)
			assert.False(t, pod.Spec.HostNetwork)
			assert.Equal(t, corev1.DNSNone, pod.Spec.DNSPolicy)
			assert.Equal(t, cfg.DNSNameservers, pod.Spec.DNSConfig.Nameservers)
			assert.False(t, *pod.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation)
			assert.Equal(t, int64(0), *pod.Spec.Containers[0].SecurityContext.RunAsUser)
			assert.Equal(t, cfg.ServiceAccountName, pod.Spec.ServiceAccountName)
			assert.Contains(t, pod.Spec.Containers[0].Args, "-isnotfc")

			secret, err := client.CoreV1().Secrets(cfg.Namespace).Get(t.Context(), secretName(request.GetSandbox().GetSandboxId()), metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, "traffic-secret", string(secret.Data[secretTrafficTokenKey]))
			assert.NotContains(t, secret.Data, "init.json", "env vars and envd credentials must not be persisted in Kubernetes")
			policy, err := client.NetworkingV1().NetworkPolicies(cfg.Namespace).Get(t.Context(), networkPolicyName(request.GetSandbox().GetSandboxId()), metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, controllerComponent, policy.Spec.Ingress[0].From[0].PodSelector.MatchLabels[componentLabel])
			assert.Equal(t, pod.Annotations[identityAnnotation], policy.Annotations[identityAnnotation])

			_, err = server.Create(t.Context(), request)
			require.NoError(t, err, "Create must be idempotent for an identical E2B request")
			assert.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestCreateRejectsSameSandboxIDWithChangedRequest(t *testing.T) {
	var calls atomic.Int32
	envd, port := newEnvdTestServer(t, http.StatusNoContent, &calls)
	defer envd.Close()
	parsed, err := url.Parse(envd.URL)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)

	cfg := testConfig(port)
	client := fake.NewSimpleClientset()
	server, err := New(client, cfg)
	require.NoError(t, err)
	server.waitForReady = markReadyWaiter(client, cfg.Namespace, host)
	request := testCreateRequest(RuntimeClassCLH)
	_, err = server.Create(t.Context(), request)
	require.NoError(t, err)

	request.EndTime = timestamppb.New(request.GetEndTime().AsTime().Add(time.Hour))
	_, err = server.Create(t.Context(), request)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
	assert.EqualValues(t, 1, calls.Load())

	_, err = client.CoreV1().Pods(cfg.Namespace).Get(t.Context(), podName(request.GetSandbox().GetSandboxId()), metav1.GetOptions{})
	require.NoError(t, err, "the conflicting retry must not delete the live sandbox")
}

func TestCreateFailureCleansOnlyNewResources(t *testing.T) {
	var calls atomic.Int32
	envd, port := newEnvdTestServer(t, http.StatusInternalServerError, &calls)
	defer envd.Close()
	parsed, err := url.Parse(envd.URL)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)

	cfg := testConfig(port)
	client := fake.NewSimpleClientset()
	server, err := New(client, cfg)
	require.NoError(t, err)
	server.waitForReady = markReadyWaiter(client, cfg.Namespace, host)
	request := testCreateRequest(RuntimeClassCLH)

	_, err = server.Create(t.Context(), request)
	require.Equal(t, codes.Unavailable, status.Code(err))
	_, err = client.CoreV1().Pods(cfg.Namespace).Get(t.Context(), podName(request.GetSandbox().GetSandboxId()), metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, err = client.CoreV1().Secrets(cfg.Namespace).Get(t.Context(), secretName(request.GetSandbox().GetSandboxId()), metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, err = client.NetworkingV1().NetworkPolicies(cfg.Namespace).Get(t.Context(), networkPolicyName(request.GetSandbox().GetSandboxId()), metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestCreateRejectsUnsupportedSemanticsBeforeCreatingResources(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	client := fake.NewSimpleClientset()
	server, err := New(client, cfg)
	require.NoError(t, err)
	request := testCreateRequest(RuntimeClassCLH)
	request.Sandbox.Snapshot = true

	_, err = server.Create(t.Context(), request)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	pods, listErr := client.CoreV1().Pods(cfg.Namespace).List(t.Context(), metav1.ListOptions{})
	require.NoError(t, listErr)
	assert.Empty(t, pods.Items)
}

func TestCreateReportsUnschedulablePodAsResourceExhausted(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	client := fake.NewSimpleClientset()
	server, err := New(client, cfg)
	require.NoError(t, err)
	server.waitForReady = func(context.Context, string) (*corev1.Pod, error) {
		return nil, fmt.Errorf("%w: insufficient memory", errPodUnschedulable)
	}

	_, err = server.Create(t.Context(), testCreateRequest(RuntimeClassCLH))
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}

func TestCreateAllowsExplicitAutoResumeOff(t *testing.T) {
	var calls atomic.Int32
	envd, port := newEnvdTestServer(t, http.StatusNoContent, &calls)
	defer envd.Close()
	parsed, err := url.Parse(envd.URL)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)

	cfg := testConfig(port)
	client := fake.NewSimpleClientset()
	server, err := New(client, cfg)
	require.NoError(t, err)
	server.waitForReady = markReadyWaiter(client, cfg.Namespace, host)
	request := testCreateRequest(RuntimeClassCLH)
	request.Sandbox.AutoResume = &orchestrator.SandboxAutoResumeConfig{Policy: "off"}

	_, err = server.Create(t.Context(), request)
	require.NoError(t, err)
}

func TestListUpdateAndDelete(t *testing.T) {
	var calls atomic.Int32
	envd, port := newEnvdTestServer(t, http.StatusNoContent, &calls)
	defer envd.Close()
	parsed, err := url.Parse(envd.URL)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(parsed.Host)
	require.NoError(t, err)

	cfg := testConfig(port)
	client := fake.NewSimpleClientset()
	server, err := New(client, cfg)
	require.NoError(t, err)
	server.waitForReady = markReadyWaiter(client, cfg.Namespace, host)
	request := testCreateRequest(RuntimeClassQEMU)
	_, err = server.Create(t.Context(), request)
	require.NoError(t, err)

	newEndTime := timestamppb.New(time.Unix(1_700_007_200, 0))
	_, err = server.Update(t.Context(), &orchestrator.SandboxUpdateRequest{
		SandboxId: request.GetSandbox().GetSandboxId(),
		EndTime:   newEndTime,
		Egress:    &orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"8.8.8.0/24"}},
	})
	require.NoError(t, err)

	list, err := server.List(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, list.GetSandboxes(), 1)
	sandbox := list.GetSandboxes()[0]
	assert.Equal(t, request.GetSandbox().GetSandboxId(), sandbox.GetSandboxId())
	assert.Equal(t, request.GetSandbox().GetTeamId(), sandbox.GetTeamId())
	assert.Equal(t, request.GetSandbox().GetExecutionId(), sandbox.GetExecutionId())
	assert.Equal(t, request.GetSandbox().GetVcpu(), sandbox.GetVcpu())
	assert.Equal(t, newEndTime.AsTime(), sandbox.GetEndTime().AsTime())

	_, err = server.Delete(t.Context(), &orchestrator.SandboxDeleteRequest{SandboxId: request.GetSandbox().GetSandboxId()})
	require.NoError(t, err)
	list, err = server.List(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Empty(t, list.GetSandboxes())
	_, err = server.Pause(t.Context(), &orchestrator.SandboxPauseRequest{})
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestDeleteKeepsPolicyUntilPodIsGone(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	cfg.DeleteTimeout = 25 * time.Millisecond
	request := testCreateRequest(RuntimeClassCLH)
	identity, err := requestIdentity(request, RuntimeClassCLH, "registry.example/e2b/python:build-42")
	require.NoError(t, err)
	pod, err := buildPod(cfg, request, RuntimeClassCLH, "registry.example/e2b/python:build-42", identity)
	require.NoError(t, err)
	policy, err := buildNetworkPolicy(cfg, request.GetSandbox(), RuntimeClassCLH, identity)
	require.NoError(t, err)
	secret, err := buildSecret(cfg, request, RuntimeClassCLH, identity)
	require.NoError(t, err)

	client := fake.NewSimpleClientset(pod, policy, secret)
	client.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		// Simulate a Pod that remains Terminating past the deletion deadline.
		return true, nil, nil
	})
	server, err := New(client, cfg)
	require.NoError(t, err)

	_, err = server.Delete(t.Context(), &orchestrator.SandboxDeleteRequest{SandboxId: request.GetSandbox().GetSandboxId()})
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
	_, err = client.NetworkingV1().NetworkPolicies(cfg.Namespace).Get(t.Context(), policy.Name, metav1.GetOptions{})
	require.NoError(t, err, "egress policy must remain while the Pod can still execute")
	_, err = client.CoreV1().Secrets(cfg.Namespace).Get(t.Context(), secret.Name, metav1.GetOptions{})
	require.NoError(t, err, "proxy credentials must remain until the Pod is gone")
}

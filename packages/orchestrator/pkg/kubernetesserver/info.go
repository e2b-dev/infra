package kubernetesserver

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type InfoServer struct {
	orchestratorinfo.UnimplementedInfoServiceServer

	client    kubernetes.Interface
	config    Config
	serviceID string
	startedAt time.Time

	mu              sync.RWMutex
	serviceStatus   orchestratorinfo.ServiceInfoStatus
	statusChangedAt time.Time
}

func NewInfo(client kubernetes.Interface, config Config) *InfoServer {
	now := time.Now().UTC()
	return &InfoServer{
		client:          client,
		config:          config,
		serviceID:       uuid.NewString(),
		startedAt:       now,
		serviceStatus:   orchestratorinfo.ServiceInfoStatus_Healthy,
		statusChangedAt: now,
	}
}

func (s *InfoServer) ServiceInfo(ctx context.Context, _ *emptypb.Empty) (*orchestratorinfo.ServiceInfoResponse, error) {
	pods, err := s.client.CoreV1().Pods(s.config.Namespace).List(ctx, metav1.ListOptions{LabelSelector: managedSandboxSelector()})
	if err != nil {
		return nil, grpcKubernetesError("list sandbox Pods for service info", err)
	}

	var allocatedCPU uint32
	var allocatedMemory, allocatedDisk uint64
	var running uint32
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}
		running++
		if value, err := strconv.ParseUint(pod.Annotations[vcpuAnnotation], 10, 32); err == nil {
			allocatedCPU += uint32(value)
		}
		if value, err := strconv.ParseUint(pod.Annotations[ramMiBAnnotation], 10, 64); err == nil {
			allocatedMemory += value * 1024 * 1024
		}
		if len(pod.Spec.Containers) > 0 {
			if quantity, ok := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceEphemeralStorage]; ok && quantity.Value() > 0 {
				allocatedDisk += uint64(quantity.Value())
			}
		}
	}

	s.mu.RLock()
	serviceStatus := s.serviceStatus
	statusChangedAt := s.statusChangedAt
	s.mu.RUnlock()

	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:                 s.config.NodeID,
		ServiceId:              s.serviceID,
		ServiceVersion:         s.config.ServiceVersion,
		ServiceCommit:          s.config.ServiceCommit,
		ServiceStatus:          serviceStatus,
		ServiceStatusChangedAt: timestamppb.New(statusChangedAt),
		ServiceRoles:           []orchestratorinfo.ServiceInfoRole{orchestratorinfo.ServiceInfoRole_Orchestrator},
		ServiceStartup:         timestamppb.New(s.startedAt),
		MachineInfo: &orchestratorinfo.MachineInfo{
			CpuArchitecture: s.config.CPUArchitecture,
			CpuFamily:       s.config.CPUFamily,
			CpuModel:        s.config.CPUModel,
			CpuModelName:    s.config.CPUModelName,
		},
		Labels: labels(s.config),

		MetricCpuAllocated:         allocatedCPU,
		MetricMemoryAllocatedBytes: allocatedMemory,
		MetricDiskAllocatedBytes:   allocatedDisk,
		MetricSandboxesRunning:     running,
		MetricCpuCount:             s.config.CapacityCPU,
		MetricMemoryTotalBytes:     s.config.CapacityMemoryMiB * 1024 * 1024,

		MetricVcpuUsed:     int64(allocatedCPU),
		MetricMemoryUsedMb: int64(allocatedMemory / (1024 * 1024)),
		MetricDiskMb:       int64(allocatedDisk / (1024 * 1024)),
	}, nil
}

func labels(config Config) []string {
	unique := map[string]struct{}{
		"kubernetes":                           {},
		consts.OrchestratorRuntimeOCIKataLabel: {},
	}
	for _, runtimeClass := range config.AllowedRuntimeClasses {
		unique[runtimeClass] = struct{}{}
	}
	for _, label := range config.NodeLabels {
		unique[label] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for label := range unique {
		result = append(result, label)
	}
	sort.Strings(result)
	return result
}

func (s *InfoServer) ServiceStatusOverride(_ context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "service status is required")
	}
	requested := req.GetServiceStatus()
	if _, ok := orchestratorinfo.ServiceInfoStatus_name[int32(requested)]; !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unknown service status %d", requested)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serviceStatus == orchestratorinfo.ServiceInfoStatus_Draining && requested == orchestratorinfo.ServiceInfoStatus_Standby {
		return nil, status.Error(codes.FailedPrecondition, "cannot change node status from draining to standby")
	}
	if requested != s.serviceStatus {
		s.serviceStatus = requested
		s.statusChangedAt = time.Now().UTC()
	}
	return &emptypb.Empty{}, nil
}

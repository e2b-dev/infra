//go:build linux

package service

import (
	"context"
	"errors"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/service/machineinfo"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Server struct {
	orchestratorinfo.UnimplementedInfoServiceServer

	info        *ServiceInfo
	sandboxes   *sandbox.Map
	hostMetrics *metrics.HostMetrics
}

func NewInfoService(info *ServiceInfo, sandboxes *sandbox.Map, hostMetrics *metrics.HostMetrics) *Server {
	return &Server{
		info:        info,
		sandboxes:   sandboxes,
		hostMetrics: hostMetrics,
	}
}

func (s *Server) ServiceInfo(ctx context.Context, _ *emptypb.Empty) (*orchestratorinfo.ServiceInfoResponse, error) {
	info := s.info

	// Get host metrics for the orchestrator
	cpuMetrics, err := s.hostMetrics.GetCPUMetrics()
	if err != nil {
		logger.L().Warn(ctx, "Failed to get host metrics", zap.Error(err))
		cpuMetrics = &metrics.CPUMetrics{}
	}

	memoryMetrics, err := s.hostMetrics.GetMemoryMetrics()
	if err != nil {
		logger.L().Warn(ctx, "Failed to get host metrics", zap.Error(err))
		memoryMetrics = &metrics.MemoryMetrics{}
	}

	diskMetrics, err := s.hostMetrics.GetDiskMetrics()
	if err != nil {
		logger.L().Warn(ctx, "Failed to get host metrics", zap.Error(err))
		diskMetrics = []metrics.DiskInfo{}
	}

	// Calculate sandbox resource allocation
	sandboxVCpuAllocated := uint32(0)
	sandboxMemoryAllocated := uint64(0)
	sandboxDiskAllocated := uint64(0)

	for _, item := range s.sandboxes.Items() {
		sandboxVCpuAllocated += uint32(item.Config.Vcpu)
		sandboxMemoryAllocated += uint64(item.Config.RamMB) * 1024 * 1024
		sandboxDiskAllocated += uint64(item.Config.TotalDiskSizeMB) * 1024 * 1024
	}

	serviceStatus, serviceStatusEpoch, _ := info.GetStatusState()

	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:                 info.ClientId,
		ServiceId:              info.ServiceId,
		ServiceStatus:          serviceStatus.Status,
		ServiceStatusChangedAt: timestamppb.New(serviceStatus.ChangedAt),
		ServiceStatusEpoch:     serviceStatusEpoch,

		ServiceVersion: info.SourceVersion,
		ServiceCommit:  info.SourceCommit,

		ServiceStartup: timestamppb.New(info.Startup),
		ServiceRoles:   info.Roles,
		MachineInfo:    convertMachineInfo(info.MachineInfo),
		Labels:         info.Labels,

		// Allocated resources to sandboxes
		MetricCpuAllocated:         sandboxVCpuAllocated,
		MetricMemoryAllocatedBytes: sandboxMemoryAllocated,
		MetricDiskAllocatedBytes:   sandboxDiskAllocated,
		MetricSandboxesRunning:     uint32(s.sandboxes.Count()),

		// Host system usage metrics
		MetricCpuPercent:      uint32(cpuMetrics.UsedPercent),
		MetricMemoryUsedBytes: memoryMetrics.UsedBytes,

		// Host system total resources
		MetricCpuCount:         cpuMetrics.Count,
		MetricMemoryTotalBytes: memoryMetrics.TotalBytes,

		// Hugepage pool metrics (page counts)
		MetricHugepagesTotal:    memoryMetrics.HugePagesTotal,
		MetricHugepagesUsed:     memoryMetrics.HugePagesUsed,
		MetricHugepagesReserved: memoryMetrics.HugePagesReserved,
		MetricHugepageSizeBytes: memoryMetrics.HugePageSizeBytes,

		// Detailed disk metrics
		MetricDisks: convertDiskMetrics(diskMetrics),

		// TODO: Remove when migrated
		MetricVcpuUsed:     int64(sandboxVCpuAllocated),
		MetricMemoryUsedMb: int64(sandboxMemoryAllocated / (1024 * 1024)),
		MetricDiskMb:       int64(sandboxDiskAllocated / (1024 * 1024)),
	}, nil
}

// convertDiskMetrics converts internal DiskInfo to protobuf DiskMetrics
func convertDiskMetrics(disks []metrics.DiskInfo) []*orchestratorinfo.DiskMetrics {
	result := make([]*orchestratorinfo.DiskMetrics, len(disks))
	for i, disk := range disks {
		result[i] = &orchestratorinfo.DiskMetrics{
			MountPoint:     disk.MountPoint,
			Device:         disk.Device,
			FilesystemType: disk.FilesystemType,
			UsedBytes:      disk.UsedBytes,
			TotalBytes:     disk.TotalBytes,
		}
	}

	return result
}

// convertDiskMetrics converts internal DiskInfo to protobuf DiskMetrics
func convertMachineInfo(machineInfo machineinfo.MachineInfo) *orchestratorinfo.MachineInfo {
	return &orchestratorinfo.MachineInfo{
		CpuArchitecture: machineInfo.Arch,
		CpuFamily:       machineInfo.Family,
		CpuModel:        machineInfo.Model,
		CpuModelName:    machineInfo.ModelName,
		CpuFlags:        machineInfo.Flags,
	}
}

func (s *Server) ServiceStatusOverride(ctx context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	logger.L().Info(ctx, "service status override request received", zap.String("status", req.GetServiceStatus().String()))
	return s.applyServiceStatusOverride(ctx, req)
}

func (s *Server) ServiceStatusOverrideFenced(ctx context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	logger.L().Info(ctx, "fenced service status override request received", zap.String("status", req.GetServiceStatus().String()))
	if expected := req.GetExpectedNodeId(); expected != "" && expected != s.info.ClientId {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator node identity changed")
	}
	if expected := req.GetExpectedServiceId(); expected != "" && expected != s.info.ServiceId {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator process identity changed")
	}
	if req.GetExpectedNodeId() == "" || req.GetExpectedServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "expected orchestrator node and process identity are required")
	}
	return s.applyServiceStatusOverride(ctx, req)
}

func (s *Server) PromoteServiceStatusFenced(ctx context.Context, req *orchestratorinfo.ServicePromotionRequest) (*emptypb.Empty, error) {
	if req.GetExpectedNodeId() == "" || req.GetExpectedServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "expected orchestrator node and process identity are required")
	}
	if req.GetExpectedStatus() != orchestratorinfo.ServiceInfoStatus_Standby {
		return nil, status.Error(codes.InvalidArgument, "expected service status must be Standby")
	}
	if req.ExpectedStatusEpoch == nil {
		return nil, status.Error(codes.InvalidArgument, "expected service status epoch is required")
	}
	if req.GetExpectedNodeId() != s.info.ClientId {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator node identity changed")
	}
	if req.GetExpectedServiceId() != s.info.ServiceId {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator process identity changed")
	}
	if err := s.info.PromoteStandby(ctx, req.GetExpectedStatusEpoch()); err != nil {
		if errors.Is(err, ErrServicePromotionStatusMismatch) || errors.Is(err, ErrServicePromotionEpochMismatch) || errors.Is(err, ErrDrainingServiceCannotBeReenabled) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to promote service status")
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) DrainServiceStatusFenced(ctx context.Context, req *orchestratorinfo.ServiceDrainRequest) (*emptypb.Empty, error) {
	if req.GetExpectedNodeId() == "" || req.GetExpectedServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "expected orchestrator node and process identity are required")
	}
	if req.GetExpectedStatus() != orchestratorinfo.ServiceInfoStatus_Healthy {
		return nil, status.Error(codes.InvalidArgument, "expected service status must be Healthy")
	}
	if req.ExpectedStatusEpoch == nil {
		return nil, status.Error(codes.InvalidArgument, "expected service status epoch is required")
	}
	if req.GetExpectedNodeId() != s.info.ClientId {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator node identity changed")
	}
	if req.GetExpectedServiceId() != s.info.ServiceId {
		return nil, status.Error(codes.FailedPrecondition, "orchestrator process identity changed")
	}
	if err := s.info.DrainHealthy(ctx, req.GetExpectedStatusEpoch()); err != nil {
		if errors.Is(err, ErrServiceDrainStatusMismatch) || errors.Is(err, ErrServiceDrainEpochMismatch) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}

		return nil, status.Error(codes.Internal, "failed to drain service status")
	}

	return &emptypb.Empty{}, nil
}

func (s *Server) applyServiceStatusOverride(ctx context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	if err := s.info.SetStatus(ctx, req.GetServiceStatus()); err != nil {
		if errors.Is(err, ErrDrainingServiceCannotBeReenabled) || errors.Is(err, ErrStandbyServiceRequiresFencedPromotion) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to update service status")
	}

	return &emptypb.Empty{}, nil
}

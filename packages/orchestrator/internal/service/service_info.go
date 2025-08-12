package service

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Server struct {
	orchestratorinfo.UnimplementedInfoServiceServer

	info      *ServiceInfo
	sandboxes *smap.Map[*sandbox.Sandbox]
}

func NewInfoService(_ context.Context, grpc *grpc.Server, info *ServiceInfo, sandboxes *smap.Map[*sandbox.Sandbox]) *Server {
	s := &Server{
		info:      info,
		sandboxes: sandboxes,
	}

	orchestratorinfo.RegisterInfoServiceServer(grpc, s)
	return s
}

func (s *Server) ServiceInfo(_ context.Context, _ *emptypb.Empty) (*orchestratorinfo.ServiceInfoResponse, error) {
	info := s.info

	// Get host metrics for the orchestrator
	cpuMetrics, err := metrics.GetCPUMetrics()
	if err != nil {
		zap.L().Warn("Failed to get host metrics", zap.Error(err))
	}

	memoryMetrics, err := metrics.GetMemoryMetrics()
	if err != nil {
		zap.L().Warn("Failed to get host metrics", zap.Error(err))
	}

	diskMetrics, err := metrics.GetDiskMetrics()
	if err != nil {
		zap.L().Warn("Failed to get host metrics", zap.Error(err))
	}

	// Calculate sandbox resource allocation
	sandboxVCpuAllocated := int64(0)
	sandboxMemoryAllocated := int64(0)
	sandboxDiskAllocated := int64(0)

	for _, item := range s.sandboxes.Items() {
		sandboxVCpuAllocated += item.Config.Vcpu
		sandboxMemoryAllocated += item.Config.RamMB * 1024 * 1024
		sandboxDiskAllocated += item.Config.TotalDiskSizeMB * 1024 * 1024
	}

	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:        info.ClientId,
		ServiceId:     info.ServiceId,
		ServiceStatus: info.GetStatus(),

		ServiceVersion: info.SourceVersion,
		ServiceCommit:  info.SourceCommit,

		ServiceStartup: timestamppb.New(info.Startup),
		ServiceRoles:   info.Roles,

		// Allocated resources to sandboxes
		MetricCpuAllocated:         sandboxVCpuAllocated,
		MetricMemoryAllocatedBytes: sandboxMemoryAllocated,
		MetricDiskAllocatedBytes:   sandboxDiskAllocated,
		MetricSandboxesRunning:     int64(s.sandboxes.Count()),

		// Host system usage metrics
		MetricCpuPercent:      int64(cpuMetrics.UsedPercent),
		MetricMemoryUsedBytes: int64(memoryMetrics.UsedBytes),

		// Host system total resources
		MetricCpuCount:         int64(cpuMetrics.Count),
		MetricMemoryTotalBytes: int64(memoryMetrics.TotalBytes),

		// Detailed disk metrics
		MetricDisks: convertDiskMetrics(diskMetrics),

		// TODO: Remove when migrated
		MetricVcpuUsed:     sandboxVCpuAllocated,
		MetricMemoryUsedMb: sandboxMemoryAllocated / (1024 * 1024),
		MetricDiskMb:       sandboxDiskAllocated / (1024 * 1024),
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
			UsedBytes:      int64(disk.UsedBytes),
			TotalBytes:     int64(disk.TotalBytes),
		}
	}
	return result
}

func (s *Server) ServiceStatusOverride(_ context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	zap.L().Info("service status override request received", zap.String("status", req.ServiceStatus.String()))
	s.info.SetStatus(req.ServiceStatus)
	return &emptypb.Empty{}, nil
}

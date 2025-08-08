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
	hostMetrics, err := metrics.GetHostMetrics()
	if err != nil {
		zap.L().Warn("Failed to get host metrics", zap.Error(err))
		// Continue with zero values if we can't get host metrics
		hostMetrics = &metrics.HostMetrics{}
	}

	// Calculate sandbox resource allocation
	sandboxVCpuAllocated := int64(0)
	sandboxMemoryAllocatedMb := int64(0)
	sandboxDiskAllocatedMb := int64(0)

	for _, item := range s.sandboxes.Items() {
		sandboxVCpuAllocated += item.Config.Vcpu
		sandboxMemoryAllocatedMb += item.Config.RamMB
		sandboxDiskAllocatedMb += item.Config.TotalDiskSizeMB
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
		MetricVcpuAllocated:     sandboxVCpuAllocated,
		MetricMemoryAllocatedMb: sandboxMemoryAllocatedMb,
		MetricDiskAllocatedMb:   sandboxDiskAllocatedMb,
		MetricSandboxesRunning:  int64(s.sandboxes.Count()),

		// Host system usage metrics
		MetricHostCpuPercent:   int64(hostMetrics.CPUUsedPercent),
		MetricHostMemoryUsedMb: hostMetrics.MemoryUsedMB,

		// Host system total resources
		MetricHostCpuCount:      hostMetrics.CPUCount,
		MetricHostMemoryTotalMb: hostMetrics.MemoryTotalMB,

		// Detailed disk metrics
		MetricHostDisks: convertDiskMetrics(hostMetrics.Disks),

		// TODO: Remove when migrated
		MetricVcpuUsed:     sandboxVCpuAllocated,
		MetricMemoryUsedMb: sandboxMemoryAllocatedMb,
		MetricDiskMb:       sandboxDiskAllocatedMb,
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
			UsedMb:         disk.UsedMB,
			TotalMb:        disk.TotalMB,
		}
	}
	return result
}

func (s *Server) ServiceStatusOverride(_ context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	zap.L().Info("service status override request received", zap.String("status", req.ServiceStatus.String()))
	s.info.SetStatus(req.ServiceStatus)
	return &emptypb.Empty{}, nil
}

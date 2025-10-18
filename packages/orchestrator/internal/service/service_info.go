package service

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

type Server struct {
	orchestratorinfo.UnimplementedInfoServiceServer

	info    *ServiceInfo
	tracker *metrics.Tracker
}

func NewInfoService(info *ServiceInfo, tracker *metrics.Tracker) *Server {
	s := &Server{
		info:    info,
		tracker: tracker,
	}

	return s
}

func (s *Server) ServiceInfo(_ context.Context, _ *emptypb.Empty) (*orchestratorinfo.ServiceInfoResponse, error) {
	info := s.info

	// Get host metrics for the orchestrator
	cpuMetrics, err := metrics.GetCPUMetrics()
	if err != nil {
		zap.L().Warn("Failed to get host metrics", zap.Error(err))
		cpuMetrics = &metrics.CPUMetrics{}
	}

	memoryMetrics, err := metrics.GetMemoryMetrics()
	if err != nil {
		zap.L().Warn("Failed to get host metrics", zap.Error(err))
		memoryMetrics = &metrics.MemoryMetrics{}
	}

	diskMetrics, err := metrics.GetDiskMetrics()
	if err != nil {
		zap.L().Warn("Failed to get host metrics", zap.Error(err))
		diskMetrics = []metrics.DiskInfo{}
	}

	// Calculate sandbox resource allocation
	allocated := s.tracker.TotalAllocated()

	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:        info.ClientId,
		ServiceId:     info.ServiceId,
		ServiceStatus: info.GetStatus(),

		ServiceVersion: info.SourceVersion,
		ServiceCommit:  info.SourceCommit,

		ServiceStartup: timestamppb.New(info.Startup),
		ServiceRoles:   info.Roles,

		// Allocated resources to sandboxes
		MetricCpuAllocated:         allocated.VCPUs,
		MetricMemoryAllocatedBytes: allocated.MemoryBytes,
		MetricDiskAllocatedBytes:   allocated.DiskBytes,
		MetricSandboxesRunning:     allocated.Sandboxes,

		// Host system usage metrics
		MetricCpuPercent:      uint32(cpuMetrics.UsedPercent),
		MetricMemoryUsedBytes: memoryMetrics.UsedBytes,

		// Host system total resources
		MetricCpuCount:         cpuMetrics.Count,
		MetricMemoryTotalBytes: memoryMetrics.TotalBytes,

		// Detailed disk metrics
		MetricDisks: convertDiskMetrics(diskMetrics),

		// TODO: Remove when migrated
		MetricVcpuUsed:     int64(allocated.VCPUs),
		MetricMemoryUsedMb: int64(allocated.MemoryBytes / (1024 * 1024)),
		MetricDiskMb:       int64(allocated.DiskBytes / (1024 * 1024)),
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

func (s *Server) ServiceStatusOverride(_ context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	zap.L().Info("service status override request received", zap.String("status", req.GetServiceStatus().String()))
	s.info.SetStatus(req.GetServiceStatus())
	return &emptypb.Empty{}, nil
}

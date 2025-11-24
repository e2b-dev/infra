package service

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service/machineinfo"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Server struct {
	orchestratorinfo.UnimplementedInfoServiceServer

	info      *ServiceInfo
	sandboxes *sandbox.Map
}

func NewInfoService(info *ServiceInfo, sandboxes *sandbox.Map) *Server {
	s := &Server{
		info:      info,
		sandboxes: sandboxes,
	}

	return s
}

func (s *Server) ServiceInfo(ctx context.Context, _ *emptypb.Empty) (*orchestratorinfo.ServiceInfoResponse, error) {
	info := s.info

	// Get host metrics for the orchestrator
	cpuMetrics, err := metrics.GetCPUMetrics()
	if err != nil {
		logger.L().Warn(ctx, "Failed to get host metrics", zap.Error(err))
		cpuMetrics = &metrics.CPUMetrics{}
	}

	memoryMetrics, err := metrics.GetMemoryMetrics()
	if err != nil {
		logger.L().Warn(ctx, "Failed to get host metrics", zap.Error(err))
		memoryMetrics = &metrics.MemoryMetrics{}
	}

	diskMetrics, err := metrics.GetDiskMetrics()
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

	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:        info.ClientId,
		ServiceId:     info.ServiceId,
		ServiceStatus: info.GetStatus(),

		ServiceVersion: info.SourceVersion,
		ServiceCommit:  info.SourceCommit,

		ServiceStartup: timestamppb.New(info.Startup),
		ServiceRoles:   info.Roles,
		MachineInfo:    convertMachineInfo(info.MachineInfo),

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
	s.info.SetStatus(ctx, req.GetServiceStatus())

	return &emptypb.Empty{}, nil
}

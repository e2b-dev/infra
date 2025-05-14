package server

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *server) ServiceInfo(_ context.Context, _ *emptypb.Empty) (*orchestrator.ServiceInfoResponse, error) {
	info := s.info

	metricVCpuUsed := int64(0)
	metricMemoryUsedMb := int64(0)
	metricDiskMb := int64(0)

	for _, item := range s.sandboxes.Items() {
		metricVCpuUsed += item.Config.Vcpu
		metricMemoryUsedMb += item.Config.RamMb
		metricDiskMb += item.Config.TotalDiskSizeMb
	}

	return &orchestrator.ServiceInfoResponse{
		NodeId:         info.ClientId,
		ServiceId:      info.ServiceId,
		ServiceVersion: info.ServiceSourceVersion,
		ServiceCommit:  info.ServiceSourceCommit,
		ServiceStatus:  info.GetStatus(),
		ServiceStartup: timestamppb.New(info.ServiceStartup),

		CanBuildTemplates: info.CanWorkAsTemplateBuilder,
		CanSpawnSandboxes: info.CanWorkAsOrchestrator,

		MetricVcpuUsed:         metricVCpuUsed,
		MetricMemoryUsedMb:     metricMemoryUsedMb,
		MetricDiskMb:           metricDiskMb,
		MetricSandboxesRunning: int64(s.sandboxes.Count()),
	}, nil
}

func (s *server) ServiceStatusOverride(_ context.Context, req *orchestrator.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	zap.L().Info("service status override request received", zap.String("status", req.ServiceStatus.String()))
	s.info.SetStatus(req.ServiceStatus)
	return &emptypb.Empty{}, nil
}

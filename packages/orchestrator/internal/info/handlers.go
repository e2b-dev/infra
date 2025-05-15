package info

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func (s *Server) ServiceInfo(_ context.Context, _ *emptypb.Empty) (*orchestratorinfo.ServiceInfoResponse, error) {
	info := s.info

	metricVCpuUsed := int64(0)
	metricMemoryUsedMb := int64(0)
	metricDiskMb := int64(0)

	for _, item := range s.sandboxes.Items() {
		metricVCpuUsed += item.Config.Vcpu
		metricMemoryUsedMb += item.Config.RamMb
		metricDiskMb += item.Config.TotalDiskSizeMb
	}

	return &orchestratorinfo.ServiceInfoResponse{
		NodeId:        info.ClientId,
		ServiceId:     info.ServiceId,
		ServiceStatus: info.GetStatus(),

		ServiceVersion: info.SourceVersion,
		ServiceCommit:  info.SourceCommit,

		ServiceStartup: timestamppb.New(info.Startup),
		ServiceRoles:   info.Roles,

		MetricVcpuUsed:         metricVCpuUsed,
		MetricMemoryUsedMb:     metricMemoryUsedMb,
		MetricDiskMb:           metricDiskMb,
		MetricSandboxesRunning: int64(s.sandboxes.Count()),
	}, nil
}

func (s *Server) ServiceStatusOverride(_ context.Context, req *orchestratorinfo.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	zap.L().Info("service status override request received", zap.String("status", req.ServiceStatus.String()))
	s.info.SetStatus(req.ServiceStatus)
	return &emptypb.Empty{}, nil
}

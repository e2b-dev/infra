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

	return &orchestrator.ServiceInfoResponse{
		NodeId:         info.ClientId,
		ServiceId:      info.ServiceId,
		ServiceVersion: info.ServiceSourceVersion,
		ServiceCommit:  info.ServiceSourceCommit,
		ServiceStatus:  info.GetStatus(),
		ServiceStartup: timestamppb.New(info.ServiceStartup),

		CanBuildTemplates: info.CanWorkAsTemplateBuilder,
		CanSpawnSandboxes: info.CanWorkAsOrchestrator,

		// todo: implement
		MetricVcpuUsed:          1,
		MetricMemoryUsedInBytes: 2,
		MetricDiskUserInByte:    3,
		MetricSandboxesRunning:  4,
	}, nil
}

func (s *server) ServiceStatusOverride(_ context.Context, req *orchestrator.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	zap.L().Info("service status override request received", zap.String("status", req.ServiceStatus.String()))
	s.info.SetStatus(req.ServiceStatus)
	return &emptypb.Empty{}, nil
}

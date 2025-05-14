package server

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (s *server) ServiceInfo(ctx context.Context, _ *emptypb.Empty) (*orchestrator.ServiceInfoResponse, error) {
	info := s.info
	status := orchestrator.ServiceInfoStatus_OrchestratorHealthy // todo

	return &orchestrator.ServiceInfoResponse{
		NodeId:         info.ClientId,
		ServiceId:      info.ServiceId,
		ServiceVersion: info.ServiceSourceVersion,
		ServiceCommit:  info.ServiceSourceCommit,
		ServiceStartup: timestamppb.New(info.ServiceStartup),
		ServiceStatus:  status,

		CanBuildTemplates: info.CanWorkAsTemplateBuilder,
		CanSpawnSandboxes: info.CanWorkAsOrchestrator,

		// todo: implement
		MetricVcpuUsed:          1,
		MetricMemoryUsedInBytes: 2,
		MetricDiskUserInByte:    3,
		MetricSandboxesRunning:  4,
	}, nil
}

func (s *server) ServiceStatusOverride(ctx context.Context, req *orchestrator.ServiceStatusChangeRequest) (*emptypb.Empty, error) {
	// todo: implement
	return &emptypb.Empty{}, nil
}

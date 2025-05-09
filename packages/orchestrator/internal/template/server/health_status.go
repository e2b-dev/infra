package server

import (
	"context"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (s *ServerStore) HealthStatus(ctx context.Context, req *emptypb.Empty) (*template_manager.HealthStatusResponse, error) {
	ctx, ctxSpan := s.tracer.Start(ctx, "health-status-request")
	defer ctxSpan.End()

	return &template_manager.HealthStatusResponse{
		Status: s.healthStatus,
	}, nil
}

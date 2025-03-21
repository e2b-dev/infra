package server

import (
	"context"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *ServerStore) HealthStatus(ctx context.Context, req *emptypb.Empty) (*template_manager.HealthStatusResponse, error) {
	ctx, ctxSpan := s.tracer.Start(ctx, "health-status-request")
	defer ctxSpan.End()

	s.logger.Debug("Template manager health status request", zap.String("status", s.healthStatus.String()))

	return &template_manager.HealthStatusResponse{
		Status: s.healthStatus,
	}, nil
}

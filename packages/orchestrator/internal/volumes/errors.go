package volumes

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func newAPIError(ctx context.Context, grpcCode codes.Code, devCode string, devMessage string, args ...any) error {
	s := status.Newf(grpcCode, devMessage, args...)
	if s2, err := s.WithDetails(&orchestrator.UserError{Code: devCode, Message: devMessage}); err != nil {
		logger.L().Error(ctx, "failed to add user error details", zap.Error(err))
	} else {
		s = s2
	}

	return s.Err()
}

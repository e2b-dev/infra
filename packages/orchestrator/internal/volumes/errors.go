package volumes

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func newAPIError(ctx context.Context, grpcCode codes.Code, devCode string, devMessage string, args ...any) error {
	message := fmt.Sprintf(devMessage, args...)

	s := status.Newf(grpcCode, message)
	if s2, err := s.WithDetails(&orchestrator.UserError{Code: devCode, Message: message}); err != nil {
		logger.L().Error(ctx, "failed to add user error details", zap.Error(err))
	} else {
		s = s2
	}

	return s.Err()
}

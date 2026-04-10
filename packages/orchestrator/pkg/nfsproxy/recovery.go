package nfsproxy

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// ErrPanic is returned when a panic is recovered.
var ErrPanic = fmt.Errorf("panic")

// Recovery intercepts panics and converts them to errors.
func Recovery() middleware.Interceptor {
	return func(ctx context.Context, op string, _ []any, next func(context.Context) ([]any, error)) (results []any, err error) {
		defer func() {
			if r := recover(); r != nil { //nolint:revive // always called via defer
				logger.L().Error(ctx, fmt.Sprintf("panic in %q nfs operation", op),
					zap.Any("panic", r),
					zap.Stack("stack"),
				)
				err = ErrPanic
			}
		}()

		return next(ctx)
	}
}

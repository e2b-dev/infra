package o11y

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Logging logs operation start/end with durations.
func Logging(skipOps map[string]bool) middleware.Interceptor {
	return func(ctx context.Context, op string, args []any, next func(context.Context) error) error {
		if skipOps[op] {
			return next(ctx)
		}

		start := time.Now()
		requestID := uuid.NewString()

		l := logger.L().With(zap.String("requestID", requestID))
		l.Debug(ctx, fmt.Sprintf("[nfs proxy] %s: start", op), zap.String("operation", op))

		err := next(ctx)

		logArgs := []zap.Field{
			zap.Duration("dur", time.Since(start)),
			zap.Any("args", args),
		}

		if err == nil {
			l.Debug(ctx, fmt.Sprintf("[nfs proxy] %s: end", op), logArgs...)
		} else {
			logArgs = append(logArgs, zap.Error(err))
			l.Warn(ctx, fmt.Sprintf("[nfs proxy] %s: end", op), logArgs...)
		}

		return err
	}
}

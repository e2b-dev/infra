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
	return func(ctx context.Context, req middleware.Request, next func(context.Context) error) error {
		op := req.Op()
		if skipOps[op] {
			return next(ctx)
		}

		start := time.Now()
		requestID := uuid.NewString()

		l := logger.L().With(zap.String("requestID", requestID))
		l.Debug(ctx, fmt.Sprintf("[nfs proxy] %s: start", op), zap.String("operation", op))

		err := next(ctx)

		logFields := []zap.Field{
			zap.Duration("dur", time.Since(start)),
		}
		logFields = append(logFields, argsToZapFields(req)...)

		if err == nil {
			l.Debug(ctx, fmt.Sprintf("[nfs proxy] %s: end", op), logFields...)
		} else {
			logFields = append(logFields, zap.Error(err))
			l.Warn(ctx, fmt.Sprintf("[nfs proxy] %s: end", op), logFields...)
		}

		return err
	}
}

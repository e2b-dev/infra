package logged

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func logStart(s string, args ...any) func(context.Context, error, ...any) {
	start := time.Now()

	return func(ctx context.Context, err error, result ...any) {
		args := []zap.Field{
			zap.Duration("dur", time.Since(start)),
			zap.Any("args", args),
			zap.Any("result", result),
		}

		var log func(context.Context, string, ...zap.Field)
		if err == nil {
			log = logger.L().Debug
		} else {
			log = logger.L().Warn
			args = append(args, zap.Error(err))
			args = append(args, zap.Stack("stack"))
		}

		log(ctx, "[nfs proxy] "+s, args...)
	}
}

package logged

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func logStart(ctx context.Context, s string, args ...any) {
	logger.L().Debug(ctx, "[nfs proxy] Starting "+s, zap.Any("args", args))
}

func logEnd(ctx context.Context, s string, args ...any) {
	logEndWithError(ctx, s, nil, args...)
}

func logEndWithError(ctx context.Context, s string, err error, args ...any) {
	if err == nil {
		logger.L().Debug(ctx, "[nfs proxy] Finishing "+s, zap.Any("return", args))

		return
	}

	logger.L().Error(ctx, fmt.Sprintf("[nfs proxy] Error in %s", s), zap.Error(err), zap.Stack("stack"))
}

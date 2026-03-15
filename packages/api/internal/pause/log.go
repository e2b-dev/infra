package pause

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func LogInitiated(ctx context.Context, sandboxID string, teamID string, reason string) {
	logger.L().Info(ctx, "sandbox_pause_initiated",
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", reason),
	)
}

func LogSuccess(ctx context.Context, sandboxID string, teamID string, reason string) {
	logger.L().Info(ctx, "sandbox_pause_result",
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", reason),
		zap.String("pause_result", "success"),
	)
}

func LogFailure(ctx context.Context, sandboxID string, teamID string, reason string, err error) {
	fields := []zap.Field{
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", reason),
		zap.String("pause_result", "failure"),
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}

	logger.L().Warn(ctx, "sandbox_pause_result", fields...)
}

func LogSkipped(ctx context.Context, sandboxID string, teamID string, reason string, skipReason string) {
	logger.L().Info(ctx, "sandbox_pause_result",
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", reason),
		zap.String("pause_result", "skipped"),
		zap.String("pause_skip_reason", skipReason),
	)
}

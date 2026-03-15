package pause

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Reason string

const (
	ReasonRequest Reason = "request"
	ReasonTimeout Reason = "timeout"
)

type SkipReason string

const (
	SkipReasonAlreadyPaused SkipReason = "already_paused"
	SkipReasonNotEvictable  SkipReason = "not_evictable"
	SkipReasonNotFound      SkipReason = "not_found"
)

func LogInitiated(ctx context.Context, sandboxID string, teamID string, reason Reason) {
	logger.L().Info(ctx, "sandbox_pause_initiated",
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", string(reason)),
	)
}

func LogSuccess(ctx context.Context, sandboxID string, teamID string, reason Reason) {
	logger.L().Info(ctx, "sandbox_pause_result",
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", string(reason)),
		zap.String("pause_result", "success"),
	)
}

func LogFailure(ctx context.Context, sandboxID string, teamID string, reason Reason, err error) {
	fields := []zap.Field{
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", string(reason)),
		zap.String("pause_result", "failure"),
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}

	logger.L().Warn(ctx, "sandbox_pause_result", fields...)
}

func LogSkipped(ctx context.Context, sandboxID string, teamID string, reason Reason, skipReason SkipReason) {
	logger.L().Info(ctx, "sandbox_pause_result",
		logger.WithSandboxID(sandboxID),
		logger.WithTeamID(teamID),
		zap.String("pause_reason", string(reason)),
		zap.String("pause_result", "skipped"),
		zap.String("pause_skip_reason", string(skipReason)),
	)
}

package proxy

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type PausedSandboxChecker interface {
	PausedInfo(ctx context.Context, sandboxId string) (PausedInfo, error)
	Resume(ctx context.Context, sandboxId string, timeoutSeconds int32) error
}

type PausedInfo struct {
	Paused           bool
	AutoResumePolicy proxy.AutoResumePolicy
}

func logSleeping(ctx context.Context, sandboxId string) {
	logger.L().Info(ctx, "im sleeping", logger.WithSandboxID(sandboxId))
}

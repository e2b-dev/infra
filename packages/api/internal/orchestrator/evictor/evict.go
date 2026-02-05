package evictor

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Evictor struct {
	store         *sandbox.Store
	removeSandbox func(ctx context.Context, sandbox sandbox.Sandbox, stateAction sandbox.StateAction) error
}

func New(
	store *sandbox.Store,
	removeSandbox func(ctx context.Context, sandbox sandbox.Sandbox, stateAction sandbox.StateAction) error,
) *Evictor {
	return &Evictor{
		store:         store,
		removeSandbox: removeSandbox,
	}
}

func (e *Evictor) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			sbxs, err := e.store.AllItems(ctx, []sandbox.State{sandbox.StateRunning}, sandbox.WithOnlyExpired(true))
			if err != nil {
				logger.L().Error(ctx, "Failed to get expired sandboxes", zap.Error(err))

				continue
			}

			for _, item := range sbxs {
				sbx := item
				go func() {
					stateAction := sandbox.StateActionKill
					if sbx.AutoPause {
						stateAction = sandbox.StateActionPause
					}

					logger.L().Debug(ctx, "Evicting sandbox", logger.WithSandboxID(sbx.SandboxID), zap.String("state_action", string(stateAction)))
					if err := e.removeSandbox(ctx, sbx, stateAction); err != nil {
						logger.L().Debug(ctx, "Evicting sandbox failed", zap.Error(err), logger.WithSandboxID(sbx.SandboxID))
					}
				}()
			}
		}
	}
}

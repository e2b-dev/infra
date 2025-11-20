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
			for _, item := range e.store.Items(nil, []sandbox.State{sandbox.StateRunning}, sandbox.WithOnlyExpired(true)) {
				go func() {
					stateAction := sandbox.StateActionKill
					if item.AutoPause {
						stateAction = sandbox.StateActionPause
					}

					zap.L().Debug("Evicting sandbox", logger.WithSandboxID(item.SandboxID), zap.String("state_action", string(stateAction)))
					if err := e.removeSandbox(ctx, item, stateAction); err != nil {
						zap.L().Debug("Evicting sandbox failed", zap.Error(err), logger.WithSandboxID(item.SandboxID))
					}
				}()
			}
		}
	}
}

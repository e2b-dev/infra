package evictor

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Evictor struct {
	store         *instance.MemoryStore
	removeSandbox func(ctx context.Context, sandbox instance.Data, stateAction instance.StateAction) error
}

func New(
	store *instance.MemoryStore,
	removeSandbox func(ctx context.Context, sandbox instance.Data, stateAction instance.StateAction) error,
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
			for _, item := range e.store.ItemsToEvict() {
				go func() {
					stateAction := instance.StateActionKill
					if item.AutoPause {
						stateAction = instance.StateActionPause
					}

					if err := e.removeSandbox(ctx, item, stateAction); err != nil {
						zap.L().Debug("Evicting sandbox failed", zap.Error(err), logger.WithSandboxID(item.SandboxID))
					}
				}()
			}
		}
	}
}

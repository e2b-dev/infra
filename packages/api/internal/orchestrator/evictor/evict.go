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
	removeSandbox func(ctx context.Context, sandbox *instance.InstanceInfo, removeType instance.RemoveType) error
}

func New(
	store *instance.MemoryStore,
	removeSandbox func(ctx context.Context, sandbox *instance.InstanceInfo, removeType instance.RemoveType) error,
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
					data := item.Data()

					removeType := instance.RemoveTypeKill
					if data.AutoPause {
						removeType = instance.RemoveTypePause
					}

					if err := e.removeSandbox(ctx, item, removeType); err != nil {
						zap.L().Debug("Evicting sandbox failed", zap.Error(err), logger.WithSandboxID(data.SandboxID))
					}
				}()
			}
		}
	}
}

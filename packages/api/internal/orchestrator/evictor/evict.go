package evictor

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Evictor struct {
	store *instance.MemoryStore
}

func New(store *instance.MemoryStore) *Evictor {
	return &Evictor{store: store}
}

func (e *Evictor) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			// Get all items from the cache before iterating over them
			// to avoid holding the lock while removing items from the cache.
			items := e.store.ExpiredItems()

			for _, item := range items {
				if item.IsExpired() && item.GetState() == instance.StateRunning {
					go func() {
						removeType := instance.RemoveTypeKill
						if item.AutoPause {
							removeType = instance.RemoveTypePause
						}

						zap.L().Debug("Evicting sandbox", logger.WithSandboxID(item.SandboxID), zap.String("remove_type", string(removeType)))

						if err := e.store.Remove(ctx, item.SandboxID, removeType); err != nil {
							zap.L().Error("Error evicting sandbox", zap.Error(err), logger.WithSandboxID(item.SandboxID))
						}
					}()
				}
			}
		}
	}
}

package evictor

import (
	"context"
	"errors"
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
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			for _, item := range e.store.ExpiredItems() {
				data := item.Data()
				if data.State != instance.StateRunning {
					return
				}

				go func() {
					var err error
					var finish func(error)
					removeType := instance.RemoveTypeKill

					// Hack, use canceled context to avoid waiting
					// or killing a sandbox that is already being paused/removed
					if data.AutoPause {
						finish, err = item.StartChangingState(cancelledCtx, instance.StatePaused)
						removeType = instance.RemoveTypePause
					} else {
						finish, err = item.StartChangingState(cancelledCtx, instance.StateKilled)
					}
					if err != nil {
						if errors.Is(err, context.Canceled) {
							// This is expected
							return
						}

						zap.L().Debug("Error evicting sandbox", zap.Error(err), logger.WithSandboxID(data.SandboxID))
						return
					}

					if finish == nil {
						zap.L().Debug("Sandbox was already removed - not evicting", logger.WithSandboxID(data.SandboxID))
						return
					}
					defer finish(err)

					zap.L().Debug("Evicting sandbox", logger.WithSandboxID(data.SandboxID), zap.String("remove_type", string(removeType)))

					if err = e.store.Remove(ctx, data.SandboxID, removeType); err != nil {
						zap.L().Debug("Evicting sandbox failed", zap.Error(err), logger.WithSandboxID(data.SandboxID))
					}
				}()
			}
		}
	}
}

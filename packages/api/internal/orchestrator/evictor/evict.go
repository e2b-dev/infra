package evictor

import (
	"context"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	pollInterval     = 50 * time.Millisecond
	concurrencyLimit = 64
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
	g := errgroup.Group{}
	g.SetLimit(concurrencyLimit)

	for {
		select {
		case <-ctx.Done():
			// Wait for in-flight evictions to finish for graceful shutdown.
			g.Wait()

			return
		case <-time.After(pollInterval):
			sbxs, err := e.store.ExpiredItems(ctx)
			if err != nil {
				logger.L().Error(ctx, "Failed to get expired sandboxes", zap.Error(err))

				continue
			}

			for _, item := range sbxs {
				g.Go(func() error {
					stateAction := sandbox.StateActionKill
					if item.AutoPause {
						stateAction = sandbox.StateActionPause
					}

					logger.L().Debug(ctx, "Evicting sandbox", logger.WithSandboxID(item.SandboxID), zap.String("state_action", stateAction.Name))
					if err := e.removeSandbox(ctx, item, stateAction); err != nil {
						logger.L().Debug(ctx, "Evicting sandbox failed", zap.Error(err), logger.WithSandboxID(item.SandboxID))
					}

					return nil
				})
			}
		}
	}
}

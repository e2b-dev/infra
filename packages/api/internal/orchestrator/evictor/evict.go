package evictor

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/api/internal/pause"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	pollInterval = 50 * time.Millisecond
)

type Evictor struct {
	store         *sandbox.Store
	removeSandbox func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error
}

func New(
	store *sandbox.Store,
	removeSandbox func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error,
) *Evictor {
	return &Evictor{
		store:         store,
		removeSandbox: removeSandbox,
	}
}

func (e *Evictor) Start(ctx context.Context) {
	g := errgroup.Group{}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Wait for in-flight evictions to finish for graceful shutdown.
			g.Wait()

			return
		case <-ticker.C:
			sbxs, err := e.store.ExpiredItems(ctx)
			if err != nil {
				logger.L().Error(ctx, "Failed to get expired sandboxes", zap.Error(err))

				continue
			}

			for _, item := range sbxs {
				g.Go(func() error {
					action := sandbox.StateActionKill
					if item.AutoPause {
						action = sandbox.StateActionPause
						pause.LogInitiated(ctx, item.SandboxID, item.TeamID.String(), pause.ReasonTimeout)
					}

					if err := e.removeSandbox(context.WithoutCancel(ctx), item.TeamID, item.SandboxID, sandbox.RemoveOpts{Action: action, Eviction: true}); err != nil {
						if action == sandbox.StateActionPause {
							switch {
							case isNotEvictableError(err):
								pause.LogSkipped(ctx, item.SandboxID, item.TeamID.String(), pause.ReasonTimeout, pause.SkipReasonNotEvictable)
							case errors.Is(err, sandbox.ErrNotFound):
								pause.LogSkipped(ctx, item.SandboxID, item.TeamID.String(), pause.ReasonTimeout, pause.SkipReasonNotFound)
							default:
								pause.LogFailure(ctx, item.SandboxID, item.TeamID.String(), pause.ReasonTimeout, err)
							}
						} else if !isKnownEvictionError(err) {
							logger.L().Debug(ctx, "Evicting sandbox failed",
								zap.Error(err),
								logger.WithSandboxID(item.SandboxID),
								logger.WithTeamID(item.TeamID.String()),
							)
						}

						return nil
					} else if action == sandbox.StateActionPause {
						pause.LogSuccess(ctx, item.SandboxID, item.TeamID.String(), pause.ReasonTimeout)
					}

					if action != sandbox.StateActionPause {
						logger.L().Debug(ctx, "Sandbox evicted", logger.WithSandboxID(item.SandboxID))
					}

					return nil
				})
			}
		}
	}
}

func isNotEvictableError(err error) bool {
	return errors.Is(err, sandbox.ErrEvictionInProgress) || errors.Is(err, sandbox.ErrEvictionNotNeeded)
}

func isKnownEvictionError(err error) bool {
	return isNotEvictableError(err) || errors.Is(err, sandbox.ErrNotFound)
}

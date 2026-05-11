package evictor

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/api/internal/pause"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	pollInterval = 50 * time.Millisecond

	// maxConcurrentEvictions caps the number of evictions that can run in
	// parallel. Excess items remain expired in the store and are picked up by
	// the next tick.
	maxConcurrentEvictions = 256
)

type Evictor struct {
	store         *sandbox.Store
	removeSandbox func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error

	// evictGroup dedupes concurrent eviction attempts for the same sandbox so
	// that overlapping ticks don't kick off multiple removeSandbox calls.
	evictGroup singleflight.Group

	// inFlight counts evictions currently being processed. Observed via the
	// in-flight gauge.
	inFlight atomic.Int64
}

func New(
	store *sandbox.Store,
	removeSandbox func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error,
	meter metric.Meter,
) (*Evictor, error) {
	e := &Evictor{
		store:         store,
		removeSandbox: removeSandbox,
	}

	if _, err := telemetry.GetObservableUpDownCounter(meter, telemetry.EvictionsRunningCounterName,
		func(_ context.Context, observer metric.Int64Observer) error {
			observer.Observe(e.inFlight.Load())

			return nil
		}); err != nil {
		return nil, fmt.Errorf("failed to create evictor in-flight gauge: %w", err)
	}

	return e, nil
}

func (e *Evictor) Start(ctx context.Context) {
	g := errgroup.Group{}
	g.SetLimit(maxConcurrentEvictions)
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
				if !g.TryGo(func() error {
					// Deduplicate eviction attempts
					e.evictGroup.Do(item.SandboxID, func() (any, error) {
						e.inFlight.Add(1)
						defer e.inFlight.Add(-1)

						e.evictSandbox(ctx, item)

						return nil, nil
					})

					return nil
				}) {
					logger.L().Debug(ctx, "Max concurrent evictions reached, skipping eviction this tick",
						logger.WithSandboxID(item.SandboxID),
						logger.WithTeamID(item.TeamID.String()),
					)
				}
			}
		}
	}
}

func (e *Evictor) evictSandbox(ctx context.Context, sbx sandbox.Sandbox) {
	action := sandbox.StateActionKill
	if sbx.AutoPause {
		action = sandbox.StateActionPause
		pause.LogInitiated(ctx, sbx.SandboxID, sbx.TeamID.String(), pause.ReasonTimeout)
	}

	if err := e.removeSandbox(context.WithoutCancel(ctx), sbx.TeamID, sbx.SandboxID, sandbox.RemoveOpts{Action: action, Eviction: true}); err != nil {
		if action == sandbox.StateActionPause {
			switch {
			case isNotEvictableError(err):
				pause.LogSkipped(ctx, sbx.SandboxID, sbx.TeamID.String(), pause.ReasonTimeout, pause.SkipReasonNotEvictable)
			case errors.Is(err, sandbox.ErrNotFound):
				pause.LogSkipped(ctx, sbx.SandboxID, sbx.TeamID.String(), pause.ReasonTimeout, pause.SkipReasonNotFound)
			default:
				pause.LogFailure(ctx, sbx.SandboxID, sbx.TeamID.String(), pause.ReasonTimeout, err)
			}
		} else if !isKnownEvictionError(err) {
			logger.L().Debug(ctx, "Evicting sandbox failed",
				zap.Error(err),
				logger.WithSandboxID(sbx.SandboxID),
				logger.WithTeamID(sbx.TeamID.String()),
			)
		}

		return
	} else if action == sandbox.StateActionPause {
		pause.LogSuccess(ctx, sbx.SandboxID, sbx.TeamID.String(), pause.ReasonTimeout)
	}

	if action != sandbox.StateActionPause {
		logger.L().Debug(ctx, "Sandbox evicted", logger.WithSandboxID(sbx.SandboxID))
	}
}

func isNotEvictableError(err error) bool {
	return errors.Is(err, sandbox.ErrEvictionInProgress) || errors.Is(err, sandbox.ErrEvictionNotNeeded)
}

func isKnownEvictionError(err error) bool {
	return isNotEvictableError(err) || errors.Is(err, sandbox.ErrNotFound)
}

package evictor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/pause"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	pollInterval               = 50 * time.Millisecond
	concurrencyRefreshInterval = 30 * time.Second
)

type Evictor struct {
	store         *sandbox.Store
	removeSandbox func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error
	featureFlags  *featureflags.Client

	concurrencyLimiter *utils.AdjustableSemaphore

	// activeEvictions tracks concurrent eviction attempts for the same sandbox
	// so that overlapping ticks don't kick off multiple removeSandbox calls.
	activeEvictions sync.Map
}

func New(
	ctx context.Context,
	store *sandbox.Store,
	removeSandbox func(ctx context.Context, teamID uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error,
	featureFlags *featureflags.Client,
	meter metric.Meter,
) (*Evictor, error) {
	initialLimit := featureFlags.IntFlag(ctx, featureflags.MaxConcurrentEvictions)
	if initialLimit <= 0 {
		initialLimit = featureflags.MaxConcurrentEvictions.Fallback()
	}

	concurrencyLimiter, err := utils.NewAdjustableSemaphore(int64(initialLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to create eviction concurrency semaphore: %w", err)
	}

	e := &Evictor{
		store:              store,
		removeSandbox:      removeSandbox,
		featureFlags:       featureFlags,
		concurrencyLimiter: concurrencyLimiter,
	}

	if _, err := telemetry.GetObservableUpDownCounter(meter, telemetry.EvictionsRunningCounterName,
		func(_ context.Context, observer metric.Int64Observer) error {
			var count int64
			e.activeEvictions.Range(func(_, _ any) bool {
				count++

				return true
			})

			observer.Observe(count)

			return nil
		}); err != nil {
		return nil, fmt.Errorf("failed to create evictor in-flight gauge: %w", err)
	}

	return e, nil
}

func (e *Evictor) Start(ctx context.Context) {
	var wg sync.WaitGroup
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	refreshTicker := time.NewTicker(concurrencyRefreshInterval)
	defer refreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Wait for in-flight evictions to finish for graceful shutdown.
			wg.Wait()

			return
		case <-refreshTicker.C:
			e.refreshConcurrencyLimit(ctx)
		case <-ticker.C:
			sbxs, err := e.store.ExpiredItems(ctx)
			if err != nil {
				logger.L().Error(ctx, "Failed to get expired sandboxes", zap.Error(err))

				continue
			}

			for _, item := range sbxs {
				// Skip if an eviction for this sandbox is already in flight.
				if _, loaded := e.activeEvictions.LoadOrStore(item.SandboxID, struct{}{}); loaded {
					continue
				}

				// Non-blocking acquire: if we're at capacity, skip and let the
				// next tick retry. Mirrors the previous errgroup.TryGo behavior.
				if !e.concurrencyLimiter.TryAcquire(1) {
					e.activeEvictions.Delete(item.SandboxID)

					logger.L().Debug(ctx, "Max concurrent evictions reached, skipping eviction this tick",
						logger.WithSandboxID(item.SandboxID),
						logger.WithTeamID(item.TeamID.String()),
					)

					continue
				}

				wg.Add(1)
				go func(item sandbox.Sandbox) {
					defer wg.Done()
					defer e.concurrencyLimiter.Release(1)
					defer e.activeEvictions.Delete(item.SandboxID)

					e.evictSandbox(ctx, item)
				}(item)
			}
		}
	}
}

func (e *Evictor) refreshConcurrencyLimit(ctx context.Context) {
	limit := e.featureFlags.IntFlag(ctx, featureflags.MaxConcurrentEvictions)
	if limit <= 0 {
		return
	}

	if err := e.concurrencyLimiter.SetLimit(int64(limit)); err != nil {
		logger.L().Error(ctx, "failed to adjust eviction concurrency semaphore",
			zap.Int("limit", limit), zap.Error(err))
	}
}

func (e *Evictor) evictSandbox(ctx context.Context, sbx sandbox.Sandbox) {
	action := sandbox.StateActionKill
	if sbx.AutoPause {
		action = sandbox.StateActionPause
		pause.LogInitiated(ctx, sbx.SandboxID, sbx.TeamID.String(), pause.ReasonTimeout)
	}

	opts := sandbox.RemoveOpts{Action: action, Eviction: true}
	switch action {
	case sandbox.StateActionKill:
		opts.Reason = sandbox.KillReasonTimeout
	case sandbox.StateActionPause:
		// Honor the sandbox's auto-pause snapshot kind: filesystem-only drops
		// memory (cold-boots on resume); otherwise a full memory snapshot.
		opts.FilesystemOnly = sbx.AutoPauseFilesystemOnly
	}

	if err := e.removeSandbox(context.WithoutCancel(ctx), sbx.TeamID, sbx.SandboxID, opts); err != nil {
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
				zap.String("kill_reason", sandbox.KillReasonTimeout.String()),
			)
		}

		return
	} else if action == sandbox.StateActionPause {
		pause.LogSuccess(ctx, sbx.SandboxID, sbx.TeamID.String(), pause.ReasonTimeout)
	}

	if action != sandbox.StateActionPause {
		logger.L().Debug(ctx, "Sandbox evicted",
			logger.WithSandboxID(sbx.SandboxID),
			zap.String("kill_reason", sandbox.KillReasonTimeout.String()),
		)
	}
}

func isNotEvictableError(err error) bool {
	return errors.Is(err, sandbox.ErrEvictionInProgress) || errors.Is(err, sandbox.ErrEvictionNotNeeded)
}

func isKnownEvictionError(err error) bool {
	return isNotEvictableError(err) || errors.Is(err, sandbox.ErrNotFound)
}

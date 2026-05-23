package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const cleanerInterval = time.Minute

// TODO: Remove once fully migrated to Redis
//
// Cleaner:
// - prunes stale entries from the two Redis sandbox indexes (`globalExpirationSet` and `globalTeamsSet`).
// - removes expired sandboxes
//
// Multi-pod safety: every operation the Cleaner triggers (ZREM/SREM of
// possibly-absent members) is idempotent. Concurrent Cleaners across pods
// produce duplicate Redis traffic, not incorrect state, so we do not take
// a distributed lock.
type Cleaner struct {
	storage *Storage
	tick    time.Duration
}

func NewCleaner(storage *Storage) *Cleaner {
	return &Cleaner{
		storage: storage,
		tick:    cleanerInterval,
	}
}

// Start blocks until ctx is cancelled
func (c *Cleaner) Start(ctx context.Context) {
	t := time.NewTicker(c.tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.L().Warn(ctx, "redis storage cleanup cycle failed", zap.Error(err))
			}
		}
	}
}

// RunOnce performs one cleanup pass. Each sub-step is independent; a failure
// in one is logged but does not abort the other.
func (c *Cleaner) RunOnce(ctx context.Context) error {
	var errs []error

	// 1. globalExpirationSet: ExpiredItems internally ZREMs members whose sandbox JSON is gone.
	// 2. evictExpired removes sandboxes whose EndTime is older than StaleCutoff;
	//    recently expired ones are left to the evictor to avoid racing it.
	expired, err := c.storage.ExpiredItems(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("expiration index sweep: %w", err))
	} else {
		c.evictExpired(ctx, expired)
	}

	// 3. globalTeamsSet: TeamsWithSandboxCount internally ZREMs teams whose
	//    per-team SCARD is 0 AND whose score is older than StaleCutoff
	//    (operations.go:268-288). Discard the returned counts.
	if _, err := c.storage.TeamsWithSandboxCount(ctx); err != nil {
		errs = append(errs, fmt.Errorf("teams index sweep: %w", err))
	}

	return errors.Join(errs...)
}

func (c *Cleaner) evictExpired(ctx context.Context, expired []sandboxtypes.Sandbox) {
	if len(expired) == 0 {
		return
	}

	logger.L().Info(ctx, "Cleaner found expired sandboxes", zap.Int("count", len(expired)))

	for _, sbx := range expired {
		if time.Since(sbx.EndTime) < sandboxtypes.StaleCutoff {
			continue
		}

		if rmErr := c.storage.Remove(context.WithoutCancel(ctx), sbx.TeamID, sbx.SandboxID); rmErr != nil {
			logger.L().Error(ctx, "Cleaner failed to remove stale expired sandbox",
				zap.Error(rmErr),
				logger.WithSandboxID(sbx.SandboxID),
				logger.WithTeamID(sbx.TeamID.String()),
			)
		}
	}
}

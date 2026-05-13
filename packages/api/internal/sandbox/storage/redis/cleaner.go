package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const cleanerInterval = time.Minute

// TODO: Remove once fully migrated to Redis
//
// Cleaner prunes stale entries from the two Redis sandbox indexes
// (`globalExpirationSet` and `globalTeamsSet`).
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
//
// Per-cycle work is bounded:
//   - ExpiredItems caps internally at expiredItemsBatchSize (256) members.
//   - TeamsWithSandboxCount is one ZRANGE + one pipelined SCARD batch.
func (c *Cleaner) RunOnce(ctx context.Context) error {
	var errs []error

	// 1. globalExpirationSet: ExpiredItems internally ZREMs members whose
	//    sandbox JSON is gone (items.go:131-135). Discard the returned
	//    sandbox list — actually evicting still-running sandboxes is the
	//    evictor's job, which in memory mode reads the memory backend.
	if _, err := c.storage.ExpiredItems(ctx); err != nil {
		errs = append(errs, fmt.Errorf("expiration index sweep: %w", err))
	}

	// 2. globalTeamsSet: TeamsWithSandboxCount internally ZREMs teams whose
	//    per-team SCARD is 0 AND whose score is older than StaleCutoff
	//    (operations.go:268-288). Discard the returned counts.
	if _, err := c.storage.TeamsWithSandboxCount(ctx); err != nil {
		errs = append(errs, fmt.Errorf("teams index sweep: %w", err))
	}

	return errors.Join(errs...)
}

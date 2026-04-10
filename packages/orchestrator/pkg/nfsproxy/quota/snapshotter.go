package quota

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/volumeusage"
)

const (
	defaultSnapshotInterval = 1 * time.Hour
)

// Snapshotter periodically captures volume usage snapshots to ClickHouse for billing.
type Snapshotter struct {
	redis    redis.UniversalClient
	tracker  *Tracker
	delivery volumeusage.Delivery
	logger   *zap.Logger

	snapshotInterval time.Duration
}

// NewSnapshotter creates a new volume usage snapshotter.
func NewSnapshotter(
	redisClient redis.UniversalClient,
	tracker *Tracker,
	delivery volumeusage.Delivery,
	logger *zap.Logger,
) *Snapshotter {
	return &Snapshotter{
		redis:            redisClient,
		tracker:          tracker,
		delivery:         delivery,
		logger:           logger,
		snapshotInterval: defaultSnapshotInterval,
	}
}

// Run starts the snapshotter loop. It captures snapshots until the context is cancelled.
func (s *Snapshotter) Run(ctx context.Context) error {
	s.logger.Info("starting volume usage snapshotter",
		zap.Duration("interval", s.snapshotInterval))

	// Take an initial snapshot
	s.captureSnapshots(ctx)

	ticker := time.NewTicker(s.snapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("volume usage snapshotter stopped")

			return ctx.Err()
		case <-ticker.C:
			s.captureSnapshots(ctx)
		}
	}
}

// captureSnapshots reads all volume usage from Redis and writes to ClickHouse.
func (s *Snapshotter) captureSnapshots(ctx context.Context) {
	now := time.Now()
	s.logger.Debug("capturing volume usage snapshots")

	// Scan for all volume usage keys
	usagePattern := VolumeUsageKey + ":*"
	iter := s.redis.Scan(ctx, 0, usagePattern, 1000).Iterator()

	var count int
	for iter.Next(ctx) {
		key := iter.Val()
		// Extract volume info from key: quota:volume:usage:{teamID}/{volumeID}
		volStr := key[len(VolumeUsageKey)+1:] // +1 for the separator

		vol, err := ParseVolumeInfo(volStr)
		if err != nil {
			s.logger.Warn("failed to parse volume info from key",
				zap.String("key", key),
				zap.Error(err))

			continue
		}

		usage, err := s.redis.Get(ctx, key).Int64()
		if err != nil {
			s.logger.Warn("failed to get usage",
				zap.String("volume", volStr),
				zap.Error(err))

			continue
		}

		// Get quota from tracker's memory (0 means unlimited)
		quota := s.tracker.GetQuota(vol)

		// Calculate blocked status: blocked if quota > 0 and usage >= quota
		blocked := quota > 0 && usage >= quota

		snapshot := volumeusage.VolumeUsageSnapshot{
			Timestamp:  now,
			TeamID:     vol.TeamID,
			VolumeID:   vol.VolumeID,
			UsageBytes: usage,
			QuotaBytes: quota,
			IsBlocked:  blocked,
		}

		if err := s.delivery.Push(snapshot); err != nil {
			s.logger.Warn("failed to push snapshot",
				zap.String("volume", volStr),
				zap.Error(err))

			continue
		}

		count++
	}

	if err := iter.Err(); err != nil {
		s.logger.Error("error scanning usage keys", zap.Error(err))
	}

	s.logger.Info("volume usage snapshots captured",
		zap.Int("count", count),
		zap.Duration("duration", time.Since(now)))
}

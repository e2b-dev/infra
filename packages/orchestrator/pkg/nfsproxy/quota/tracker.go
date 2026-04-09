package quota

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	sharedquota "github.com/e2b-dev/infra/packages/shared/pkg/quota"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	// Default intervals
	defaultBlockedCacheRefresh = 30 * time.Second
)

// Re-export shared types for convenience
type VolumeInfo = sharedquota.VolumeInfo

// ParseVolumeInfo is re-exported for convenience
var ParseVolumeInfo = sharedquota.ParseVolumeInfo

// Tracker handles dirty volume tracking and quota enforcement.
type Tracker struct {
	redis  redis.UniversalClient
	logger *zap.Logger

	// Local cache for blocked status
	mu           sync.RWMutex
	blockedCache map[string]bool
	cacheExpiry  time.Time

	blockedCacheRefresh time.Duration
}

// NewTracker creates a new quota tracker.
// If redis is nil, tracking is disabled (noop mode).
func NewTracker(redisClient redis.UniversalClient, logger *zap.Logger) *Tracker {
	t := &Tracker{
		redis:               redisClient,
		logger:              logger,
		blockedCache:        make(map[string]bool),
		blockedCacheRefresh: defaultBlockedCacheRefresh,
	}

	return t
}

// MarkDirty marks a volume as needing a usage scan.
// This is fire-and-forget - errors are logged but not returned.
func (t *Tracker) MarkDirty(ctx context.Context, vol VolumeInfo) {
	if t.redis == nil {
		return
	}

	// ZADD NX - only add if not already in set (prevents duplicate scans)
	member := vol.String()
	score := float64(time.Now().Unix())

	err := t.redis.ZAddNX(ctx, sharedquota.DirtyVolumesKey, redis.Z{
		Score:  score,
		Member: member,
	}).Err()
	if err != nil {
		t.logger.Warn("failed to mark volume dirty",
			zap.String("volume", member),
			zap.Error(err))
	}
}

// IsBlocked checks if a volume is blocked due to quota exceeded.
// Uses local cache to avoid Redis round-trips on every write.
// Returns false (allow) on any error (fail open).
func (t *Tracker) IsBlocked(ctx context.Context, vol VolumeInfo) bool {
	if t.redis == nil {
		return false
	}

	t.mu.RLock()
	if time.Now().Before(t.cacheExpiry) {
		blocked := t.blockedCache[vol.String()]
		t.mu.RUnlock()

		return blocked
	}
	t.mu.RUnlock()

	// Cache expired, refresh it
	t.refreshBlockedCache(ctx)

	t.mu.RLock()
	blocked := t.blockedCache[vol.String()]
	t.mu.RUnlock()

	return blocked
}

// refreshBlockedCache fetches all blocked flags from Redis.
func (t *Tracker) refreshBlockedCache(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check again under write lock (another goroutine might have refreshed)
	if time.Now().Before(t.cacheExpiry) {
		return
	}

	// Scan for all blocked keys
	pattern := redis_utils.CreateKey(sharedquota.VolumeBlockedKey, "*")
	iter := t.redis.Scan(ctx, 0, pattern, 1000).Iterator()

	newCache := make(map[string]bool)
	for iter.Next(ctx) {
		key := iter.Val()
		// Extract volume info from key: quota:volume:blocked:{teamID}/{volumeID}
		volStr := key[len(sharedquota.VolumeBlockedKey)+1:] // +1 for the separator

		blocked, err := t.redis.Get(ctx, key).Bool()
		if err != nil {
			continue // fail open
		}
		newCache[volStr] = blocked
	}

	if err := iter.Err(); err != nil {
		t.logger.Warn("failed to scan blocked keys", zap.Error(err))
		// Keep old cache on error, just extend expiry slightly
		t.cacheExpiry = time.Now().Add(5 * time.Second)

		return
	}

	t.blockedCache = newCache
	t.cacheExpiry = time.Now().Add(t.blockedCacheRefresh)
}

// SetBlocked sets the blocked status for a volume.
// Called by the scanner after checking usage against quota.
func (t *Tracker) SetBlocked(ctx context.Context, vol VolumeInfo, blocked bool) error {
	if t.redis == nil {
		return nil
	}

	key := redis_utils.CreateKey(sharedquota.VolumeBlockedKey, vol.String())

	return t.redis.Set(ctx, key, blocked, 0).Err()
}

// SetUsage sets the current usage for a volume.
// Called by the scanner after measuring disk usage.
func (t *Tracker) SetUsage(ctx context.Context, vol VolumeInfo, usageBytes int64) error {
	if t.redis == nil {
		return nil
	}

	key := redis_utils.CreateKey(sharedquota.VolumeUsageKey, vol.String())

	return t.redis.Set(ctx, key, usageBytes, 0).Err()
}

// GetUsage gets the current usage for a volume.
func (t *Tracker) GetUsage(ctx context.Context, vol VolumeInfo) (int64, error) {
	if t.redis == nil {
		return 0, nil
	}

	key := redis_utils.CreateKey(sharedquota.VolumeUsageKey, vol.String())

	return t.redis.Get(ctx, key).Int64()
}

// GetQuota gets the quota limit for a volume.
func (t *Tracker) GetQuota(ctx context.Context, vol VolumeInfo) (int64, error) {
	if t.redis == nil {
		return 0, nil
	}

	key := redis_utils.CreateKey(sharedquota.VolumeQuotaKey, vol.String())

	return t.redis.Get(ctx, key).Int64()
}

// SetQuota sets the quota limit for a volume.
func (t *Tracker) SetQuota(ctx context.Context, vol VolumeInfo, quotaBytes int64) error {
	if t.redis == nil {
		return nil
	}

	key := redis_utils.CreateKey(sharedquota.VolumeQuotaKey, vol.String())

	return t.redis.Set(ctx, key, quotaBytes, 0).Err()
}

// PopDirtyVolume atomically pops the oldest dirty volume from the queue.
// Returns the volume info and true if found, or empty info and false if queue is empty.
func (t *Tracker) PopDirtyVolume(ctx context.Context) (VolumeInfo, bool, error) {
	if t.redis == nil {
		return VolumeInfo{}, false, nil
	}

	// ZPOPMIN returns the member with lowest score (oldest timestamp)
	result, err := t.redis.ZPopMin(ctx, sharedquota.DirtyVolumesKey, 1).Result()
	if err != nil {
		return VolumeInfo{}, false, err
	}

	if len(result) == 0 {
		return VolumeInfo{}, false, nil
	}

	member := result[0].Member.(string)
	vol, err := ParseVolumeInfo(member)
	if err != nil {
		return VolumeInfo{}, false, err
	}

	return vol, true, nil
}

// BlockingPopDirtyVolume waits for a dirty volume to be available.
// Returns context error if context is cancelled.
func (t *Tracker) BlockingPopDirtyVolume(ctx context.Context, timeout time.Duration) (VolumeInfo, bool, error) {
	if t.redis == nil {
		// In noop mode, just sleep and return nothing
		select {
		case <-ctx.Done():
			return VolumeInfo{}, false, ctx.Err()
		case <-time.After(timeout):
			return VolumeInfo{}, false, nil
		}
	}

	result, err := t.redis.BZPopMin(ctx, timeout, sharedquota.DirtyVolumesKey).Result()
	if errors.Is(err, redis.Nil) {
		return VolumeInfo{}, false, nil
	}
	if err != nil {
		return VolumeInfo{}, false, err
	}

	member := result.Member.(string)
	vol, err := ParseVolumeInfo(member)
	if err != nil {
		return VolumeInfo{}, false, err
	}

	return vol, true, nil
}

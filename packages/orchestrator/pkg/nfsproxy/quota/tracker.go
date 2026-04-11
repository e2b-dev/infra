package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// Redis key prefixes for quota management
const (
	DirtyVolumesKey = "quota:dirty_volumes"
	VolumeUsageKey  = "quota:volume:usage"
)

const (
	// Default intervals
	defaultUsageCacheRefresh = 30 * time.Second
)

// VolumeInfo identifies a volume for quota tracking.
type VolumeInfo struct {
	TeamID   uuid.UUID
	VolumeID uuid.UUID
	Quota    int64 // Quota in bytes. 0 means unlimited.
}

// String returns the canonical string representation for Redis keys.
func (v VolumeInfo) String() string {
	return fmt.Sprintf("%s/%s", v.TeamID.String(), v.VolumeID.String())
}

// ParseVolumeInfo parses a "teamID/volumeID" string into a VolumeInfo.
// Note: Quota is not included in the string representation and will be zero.
func ParseVolumeInfo(s string) (VolumeInfo, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return VolumeInfo{}, fmt.Errorf("invalid format: expected 'teamID/volumeID', got %q", s)
	}

	teamID, err := uuid.Parse(parts[0])
	if err != nil {
		return VolumeInfo{}, fmt.Errorf("invalid team ID: %w", err)
	}

	volumeID, err := uuid.Parse(parts[1])
	if err != nil {
		return VolumeInfo{}, fmt.Errorf("invalid volume ID: %w", err)
	}

	return VolumeInfo{TeamID: teamID, VolumeID: volumeID}, nil
}

// Tracker handles dirty volume tracking and quota enforcement.
type Tracker struct {
	redis  redis.UniversalClient
	logger *zap.Logger

	// Local cache for usage values (refreshed periodically from Redis)
	mu          sync.RWMutex
	usageCache  map[string]int64 // volume string -> usage bytes
	quotaCache  map[string]int64 // volume string -> quota bytes (0 = unlimited)
	cacheExpiry time.Time

	usageCacheRefresh time.Duration
}

// NewTracker creates a new quota tracker.
// If redis is nil, tracking is disabled (noop mode).
func NewTracker(redisClient redis.UniversalClient, logger *zap.Logger) *Tracker {
	t := &Tracker{
		redis:             redisClient,
		logger:            logger,
		usageCache:        make(map[string]int64),
		quotaCache:        make(map[string]int64),
		usageCacheRefresh: defaultUsageCacheRefresh,
	}

	return t
}

// SetUsageCacheRefresh sets the cache refresh duration.
// This is primarily useful for testing with shorter durations.
func (t *Tracker) SetUsageCacheRefresh(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.usageCacheRefresh = d
}

// ExpireCacheForTesting immediately expires the usage cache.
// This forces the next IsBlocked call to refresh from Redis.
func (t *Tracker) ExpireCacheForTesting() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cacheExpiry = time.Time{}
}

// RegisterVolume registers a volume's quota for blocking checks.
// This should be called when a volume is mounted.
func (t *Tracker) RegisterVolume(vol VolumeInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.quotaCache[vol.String()] = vol.Quota
}

// UnregisterVolume removes a volume from quota tracking.
// This should be called when a volume is unmounted.
func (t *Tracker) UnregisterVolume(vol VolumeInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.quotaCache, vol.String())
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

	err := t.redis.ZAddNX(ctx, DirtyVolumesKey, redis.Z{
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
// Returns false (allow) on any error or if quota is 0 (unlimited).
func (t *Tracker) IsBlocked(ctx context.Context, vol VolumeInfo) bool {
	if t.redis == nil {
		return false
	}

	t.mu.RLock()
	quota, hasQuota := t.quotaCache[vol.String()]
	if !hasQuota || quota == 0 {
		// No quota registered or unlimited - not blocked
		t.mu.RUnlock()

		return false
	}

	if time.Now().Before(t.cacheExpiry) {
		usage := t.usageCache[vol.String()]
		t.mu.RUnlock()

		return usage >= quota
	}
	t.mu.RUnlock()

	// Cache expired, refresh it
	t.refreshUsageCache(ctx)

	t.mu.RLock()
	usage := t.usageCache[vol.String()]
	// Re-read quota in case it changed
	quota = t.quotaCache[vol.String()]
	t.mu.RUnlock()

	if quota == 0 {
		return false
	}

	return usage >= quota
}

// refreshUsageCache fetches all usage values from Redis.
func (t *Tracker) refreshUsageCache(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check again under write lock (another goroutine might have refreshed)
	if time.Now().Before(t.cacheExpiry) {
		return
	}

	// Scan for all usage keys
	pattern := redis_utils.CreateKey(VolumeUsageKey, "*")
	iter := t.redis.Scan(ctx, 0, pattern, 1000).Iterator()

	newCache := make(map[string]int64)
	for iter.Next(ctx) {
		key := iter.Val()
		// Extract volume info from key: quota:volume:usage:{teamID}/{volumeID}
		volStr := key[len(VolumeUsageKey)+1:] // +1 for the separator

		usage, err := t.redis.Get(ctx, key).Int64()
		if err != nil {
			continue
		}
		newCache[volStr] = usage
	}

	if err := iter.Err(); err != nil {
		t.logger.Warn("failed to scan usage keys", zap.Error(err))
		// Keep old cache on error, just extend expiry slightly
		t.cacheExpiry = time.Now().Add(5 * time.Second)

		return
	}

	t.usageCache = newCache
	t.cacheExpiry = time.Now().Add(t.usageCacheRefresh)
}

// SetUsage sets the current usage for a volume.
// Called by the scanner after measuring disk usage.
func (t *Tracker) SetUsage(ctx context.Context, vol VolumeInfo, usageBytes int64) error {
	if t.redis == nil {
		return nil
	}

	key := redis_utils.CreateKey(VolumeUsageKey, vol.String())

	return t.redis.Set(ctx, key, usageBytes, 0).Err()
}

// GetUsage gets the current usage for a volume.
func (t *Tracker) GetUsage(ctx context.Context, vol VolumeInfo) (int64, error) {
	if t.redis == nil {
		return 0, nil
	}

	key := redis_utils.CreateKey(VolumeUsageKey, vol.String())

	return t.redis.Get(ctx, key).Int64()
}

// GetQuota gets the quota limit for a volume from memory.
func (t *Tracker) GetQuota(vol VolumeInfo) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.quotaCache[vol.String()]
}

// PopDirtyVolume atomically pops the oldest dirty volume from the queue.
// Returns the volume info and true if found, or empty info and false if queue is empty.
func (t *Tracker) PopDirtyVolume(ctx context.Context) (VolumeInfo, bool, error) {
	if t.redis == nil {
		return VolumeInfo{}, false, nil
	}

	// ZPOPMIN returns the member with lowest score (oldest timestamp)
	result, err := t.redis.ZPopMin(ctx, DirtyVolumesKey, 1).Result()
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

	result, err := t.redis.BZPopMin(ctx, timeout, DirtyVolumesKey).Result()
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

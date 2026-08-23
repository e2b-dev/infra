package sandboxcountscache

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
)

const (
	countsCacheTTL             = 30 * time.Second
	countsCacheRefreshInterval = 5 * time.Second
	countsCacheRefreshTimeout  = 10 * time.Second
	countsCacheLockTTL         = countsCacheRefreshTimeout + 2*cache.RedisDefaultTimeout

	countsCacheKeyPrefix = "sandbox:team-running-counts"

	// countsCacheKey is the single fixed key: every consumer needs the whole
	// fleet snapshot, so one entry means one lock and one refresh per interval
	// across all API instances.
	countsCacheKey = "all"
)

// Source provides the live per-team running sandbox counts.
type Source interface {
	TeamRunningSandboxCounts(ctx context.Context) (map[uuid.UUID]int64, error)
}

// CountsCache shares one fleet-wide count snapshot across API instances.
type CountsCache struct {
	source Source
	cache  *cache.RedisCache[map[uuid.UUID]int64]
}

func NewCountsCache(source Source, redisClient redis.UniversalClient) *CountsCache {
	return &CountsCache{
		source: source,
		cache: cache.NewRedisCache(cache.RedisConfig[map[uuid.UUID]int64]{
			RedisClient:     redisClient,
			TTL:             countsCacheTTL,
			RefreshInterval: countsCacheRefreshInterval,
			RefreshTimeout:  countsCacheRefreshTimeout,
			LockTTL:         countsCacheLockTTL,
			RedisPrefix:     countsCacheKeyPrefix,
		}),
	}
}

func (c *CountsCache) TeamRunningSandboxCounts(ctx context.Context) (map[uuid.UUID]int64, error) {
	return c.cache.GetOrSet(
		ctx,
		countsCacheKey,
		func(ctx context.Context, _ string) (map[uuid.UUID]int64, error) {
			return c.source.TeamRunningSandboxCounts(ctx)
		},
	)
}

package auth

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
)

const (
	authInfoExpiration = 5 * time.Minute
	refreshInterval    = 1 * time.Minute
	refreshTimeout     = 30 * time.Second

	authCacheRedisPrefix = "auth:team"
)

// AuthCache is a Redis-backed TTL cache for authentication data (teams, users, etc.).
type AuthCache[T any] struct {
	cache *cache.RedisCache[T]
}

// NewAuthCache creates a new Redis-backed AuthCache with default TTL and refresh settings.
func NewAuthCache[T any](redisClient redis.UniversalClient) *AuthCache[T] {
	return &AuthCache[T]{
		cache: cache.NewRedisCache(cache.RedisConfig[T]{
			RedisClient:     redisClient,
			TTL:             authInfoExpiration,
			RefreshInterval: refreshInterval,
			RefreshTimeout:  refreshTimeout,
			RedisPrefix:     authCacheRedisPrefix,
		}),
	}
}

// GetOrSet returns the cached value for the key, or calls dataCallback to populate it.
func (c *AuthCache[T]) GetOrSet(ctx context.Context, key string, dataCallback func(ctx context.Context, key string) (T, error)) (T, error) {
	return c.cache.GetOrSet(ctx, key, dataCallback)
}

// Invalidate removes a single entry from the cache by key.
func (c *AuthCache[T]) Invalidate(ctx context.Context, key string) {
	c.cache.Delete(ctx, key)
}

// Close is a no-op for the Redis-backed cache (no background goroutines).
func (c *AuthCache[T]) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}

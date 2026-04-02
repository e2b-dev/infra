package auth

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
)

const (
	authInfoExpiration = 5 * time.Minute
	refreshInterval    = 1 * time.Minute
	refreshTimeout     = 30 * time.Second
	callbackTimeout    = 30 * time.Second
)

// AuthCache is a generic TTL cache for authentication data (teams, users, etc.).
type AuthCache[T any] struct {
	cache *cache.MemoryCache[T]
}

// NewAuthCache creates a new AuthCache with default TTL and refresh settings.
func NewAuthCache[T any]() *AuthCache[T] {
	config := cache.Config[T]{
		TTL:             authInfoExpiration,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  refreshTimeout,
		CallbackTimeout: callbackTimeout,
	}

	return &AuthCache[T]{
		cache: cache.NewMemoryCache(config),
	}
}

// GetOrSet returns the cached value for the key, or calls dataCallback to populate it.
func (c *AuthCache[T]) GetOrSet(ctx context.Context, key string, dataCallback func(ctx context.Context, key string) (T, error)) (T, error) {
	return c.cache.GetOrSet(ctx, key, dataCallback)
}

// Invalidate removes a single entry from the cache by key.
func (c *AuthCache[T]) Invalidate(key string) {
	c.cache.Delete(key)
}

// Close stops the cache's background refresh goroutines.
func (c *AuthCache[T]) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}

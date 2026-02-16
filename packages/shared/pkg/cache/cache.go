package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sync/singleflight"
)

type (
	// DataCallback is a function that fetches data for a given key
	DataCallback[V any] func(ctx context.Context, key string) (V, error)

	// ExtractKeyFunc is a function that extracts a key from a value
	ExtractKeyFunc[V any] func(value V) string
)

// Config holds the configuration for a Cache
type Config[V any] struct {
	// TTL is the time-to-live for cache entries
	TTL time.Duration
	// RefreshInterval is the interval at which cache entries are refreshed in the background
	// If 0, no background refresh is performed
	RefreshInterval time.Duration
	// CallbackTimeout is the timeout for the data callback function
	// Defaults to 30 seconds if not specified
	CallbackTimeout time.Duration
	// RefreshTimeout is the timeout for refresh operations
	// Defaults to 30 seconds if not specified
	RefreshTimeout time.Duration
	// ExtractKeyFunc is an optional function to extract a key from the value
	ExtractKeyFunc ExtractKeyFunc[V]
}

// Item wraps a cached value with metadata for refresh tracking
type Item[V any] struct {
	value       V
	lastRefresh time.Time
	once        singleflight.Group
}

// Cache is a generic cache with optional background refresh support
type Cache[V any] struct {
	cache      *ttlcache.Cache[string, *Item[V]]
	config     Config[V]
	fetchGroup singleflight.Group // deduplicates concurrent cache miss fetches
}

// NewCache creates a new Cache with the given configuration
func NewCache[V any](config Config[V]) *Cache[V] {
	cache := ttlcache.New(ttlcache.WithTTL[string, *Item[V]](config.TTL))
	go cache.Start()

	if config.RefreshTimeout == 0 {
		config.RefreshTimeout = 30 * time.Second
	}

	if config.CallbackTimeout == 0 {
		config.CallbackTimeout = 30 * time.Second
	}

	return &Cache[V]{
		cache:  cache,
		config: config,
	}
}

// GetWithoutTouch retrieves a value from the cache by key
func (c *Cache[V]) GetWithoutTouch(key string) (V, bool) {
	item := c.cache.Get(key, ttlcache.WithDisableTouchOnHit[string, *Item[V]]())
	if item == nil {
		var zero V

		return zero, false
	}

	return item.Value().value, true
}

// Set stores a value in the cache with the default TTL
func (c *Cache[V]) Set(key string, value V) {
	c.cache.Set(key, &Item[V]{
		value:       value,
		lastRefresh: time.Now(),
	}, c.config.TTL)
}

// Delete removes a value from the cache
func (c *Cache[V]) Delete(key string) {
	c.cache.Delete(key)
}

// GetOrSet retrieves a value from the cache, or fetches it using the callback if not present
// If RefreshInterval is configured and the cached value is older than the interval,
// it triggers a background refresh while returning the stale value
func (c *Cache[V]) GetOrSet(ctx context.Context, key string, dataCallback DataCallback[V]) (V, error) {
	item := c.cache.Get(key)

	// Cache miss - fetch with singleflight to deduplicate concurrent requests
	if item == nil {
		result, err, _ := c.fetchGroup.Do(key, func() (any, error) {
			// Use a non-cancellable context for the data fetch to ensure short context won't cause all the requests to fail
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.config.CallbackTimeout)
			defer cancel()

			return c.getAndSet(ctx, key, dataCallback)
		})
		if err != nil {
			var zero V

			return zero, err
		}

		return result.(V), nil
	}

	cacheItem := item.Value()

	// If refresh interval is configured and data is stale, trigger background refresh
	if c.config.RefreshInterval > 0 && time.Since(cacheItem.lastRefresh) > c.config.RefreshInterval {
		go cacheItem.once.Do(fmt.Sprint(key), func() (any, error) { //nolint:unparam // we don't control the api
			c.refresh(context.WithoutCancel(ctx), key, dataCallback)

			return nil, nil
		})
	}

	return cacheItem.value, nil
}

func (c *Cache[V]) getAndSet(ctx context.Context, key string, dataCallback DataCallback[V]) (V, error) {
	// Double-check cache in case another goroutine just populated it
	// This is necessary as there would still be a race condition
	if existingItem := c.cache.Get(key); existingItem != nil {
		return existingItem.Value().value, nil
	}

	value, err := dataCallback(ctx, key)
	if err != nil {
		return value, err
	}

	if c.config.ExtractKeyFunc != nil {
		key = c.config.ExtractKeyFunc(value)
	}

	c.cache.Set(key, &Item[V]{
		value:       value,
		lastRefresh: time.Now(),
	}, c.config.TTL)

	return value, nil
}

func (c *Cache[V]) Keys() []string {
	return c.cache.Keys()
}

// refresh refreshes the cache for the given key in the background
func (c *Cache[V]) refresh(ctx context.Context, key string, dataCallback DataCallback[V]) {
	ctx, cancel := context.WithTimeout(ctx, c.config.RefreshTimeout)
	defer cancel()

	value, err := dataCallback(ctx, key)
	if err != nil {
		// On refresh error, delete the cache entry to force a fresh fetch next time
		c.cache.Delete(key)

		return
	}

	if c.config.ExtractKeyFunc != nil {
		key = c.config.ExtractKeyFunc(value)
	}

	c.cache.Set(key, &Item[V]{
		value:       value,
		lastRefresh: time.Now(),
	}, c.config.TTL)
}

func (c *Cache[V]) Close(_ context.Context) error {
	c.cache.Stop()

	return nil
}

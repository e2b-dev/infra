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
	DataCallback[K comparable, V any] func(ctx context.Context, key K) (V, error)

	// ExtractKeyFunc is a function that extracts a key from a value
	ExtractKeyFunc[K comparable, V any] func(value V) K
)

// Config holds the configuration for a Cache
type Config[K comparable, V any] struct {
	// TTL is the time-to-live for cache entries
	TTL time.Duration
	// RefreshInterval is the interval at which cache entries are refreshed in the background
	// If 0, no background refresh is performed
	RefreshInterval time.Duration
	// RefreshTimeout is the timeout for refresh operations
	// Defaults to 30 seconds if not specified
	RefreshTimeout time.Duration
	// ExtractKeyFunc is an optional function to extract a key from the value
	ExtractKeyFunc ExtractKeyFunc[K, V]
}

// Item wraps a cached value with metadata for refresh tracking
type Item[V any] struct {
	value       V
	lastRefresh time.Time
	once        singleflight.Group
}

// Cache is a generic cache with optional background refresh support
type Cache[K comparable, V any] struct {
	cache  *ttlcache.Cache[K, *Item[V]]
	config Config[K, V]
}

// NewCache creates a new Cache with the given configuration
func NewCache[K comparable, V any](config Config[K, V]) *Cache[K, V] {
	cache := ttlcache.New(ttlcache.WithTTL[K, *Item[V]](config.TTL))
	go cache.Start()

	if config.RefreshTimeout == 0 {
		config.RefreshTimeout = 30 * time.Second
	}

	return &Cache[K, V]{
		cache:  cache,
		config: config,
	}
}

// Get retrieves a value from the cache by key
func (c *Cache[K, V]) Get(key K) (V, bool) {
	item := c.cache.Get(key)
	if item == nil {
		var zero V

		return zero, false
	}

	return item.Value().value, true
}

// Set stores a value in the cache with the default TTL
func (c *Cache[K, V]) Set(key K, value V) {
	c.cache.Set(key, &Item[V]{
		value:       value,
		lastRefresh: time.Now(),
	}, c.config.TTL)
}

// Delete removes a value from the cache
func (c *Cache[K, V]) Delete(key K) {
	c.cache.Delete(key)
}

// GetOrSet retrieves a value from the cache, or fetches it using the callback if not present
// If RefreshInterval is configured and the cached value is older than the interval,
// it triggers a background refresh while returning the stale value
func (c *Cache[K, V]) GetOrSet(ctx context.Context, key K, dataCallback DataCallback[K, V]) (V, error) {
	item := c.cache.Get(key)

	// Cache miss - fetch immediately
	if item == nil {
		value, err := dataCallback(ctx, key)
		if err != nil {
			var zero V

			return zero, err
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

	cacheItem := item.Value()

	// If refresh interval is configured and data is stale, trigger background refresh
	if c.config.RefreshInterval > 0 && time.Since(cacheItem.lastRefresh) > c.config.RefreshInterval {
		go cacheItem.once.Do(fmt.Sprint(key), func() (any, error) {
			c.refresh(context.WithoutCancel(ctx), key, dataCallback)

			return nil, nil
		})
	}

	return cacheItem.value, nil
}

func (c *Cache[K, V]) Keys() []K {
	return c.cache.Keys()
}

// refresh refreshes the cache for the given key in the background
func (c *Cache[K, V]) refresh(ctx context.Context, key K, dataCallback DataCallback[K, V]) {
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

func (c *Cache[K, V]) Close(_ context.Context) error {
	c.cache.Stop()

	return nil
}

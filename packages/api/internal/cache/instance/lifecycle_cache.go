package instance

import (
	"context"
	"time"

	cmap "github.com/orcaman/concurrent-map/v2"
)

type LifecycleCacheItem interface {
	IsExpired() bool
	SetExpired()
}

type lifecycleCacheMetrics struct {
	Evictions uint64
}

type lifecycleCache[T LifecycleCacheItem] struct {
	items cmap.ConcurrentMap[string, T]
	// evicting is used to track items in the process of evicting.
	// This is to allow checking if an item is still in the process as that might take some time.
	evicting cmap.ConcurrentMap[string, struct{}]
	onEvict  func(ctx context.Context, instance T)

	metrics lifecycleCacheMetrics
}

func newLifecycleCache[T LifecycleCacheItem]() *lifecycleCache[T] {
	return &lifecycleCache[T]{
		items:    cmap.New[T](),
		evicting: cmap.New[struct{}](),
		onEvict:  func(ctx context.Context, instance T) {},
		metrics: lifecycleCacheMetrics{
			Evictions: 0,
		},
	}
}

func (c *lifecycleCache[T]) OnEviction(onEvict func(ctx context.Context, instance T)) {
	c.onEvict = onEvict
}

func (c *lifecycleCache[T]) Metrics() lifecycleCacheMetrics {
	return c.metrics
}

func (c *lifecycleCache[T]) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			// Get all items from the cache before iterating over them
			// to avoid holding the lock while removing items from the cache.
			items := c.items.Items()

			for key, value := range items {
				if value.IsExpired() {
					absent := c.evicting.SetIfAbsent(key, struct{}{})
					if absent {
						go func() {
							c.onEvict(ctx, value)
							c.items.Remove(key)
							c.evicting.Remove(key)
						}()

						c.metrics.Evictions += 1
					}
				}
			}
		}
	}
}

func (c *lifecycleCache[T]) SetIfAbsent(key string, value T) bool {
	return c.items.SetIfAbsent(key, value)
}

func (c *lifecycleCache[T]) Has(key string, includeExpired bool) bool {
	if includeExpired {
		ok := c.evicting.Has(key)

		if ok {
			return true
		}

		return c.items.Has(key)
	}

	_, ok := c.Get(key)
	return ok
}

func (c *lifecycleCache[T]) Get(key string) (T, bool) {
	var zero T
	value, ok := c.items.Get(key)
	if !ok {
		return zero, false
	}

	if value.IsExpired() {
		return zero, false
	}

	return value, true
}

func (c *lifecycleCache[T]) Remove(key string) {
	c.items.Remove(key)
}

func (c *lifecycleCache[T]) Items() (items []T) {
	for _, item := range c.items.Items() {
		if item.IsExpired() {
			continue
		}

		items = append(items, item)
	}
	return items
}

func (c *lifecycleCache[T]) Len() int {
	return len(c.Items())
}

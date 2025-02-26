package instance

import (
	"context"
	"time"

	cmap "github.com/orcaman/concurrent-map/v2"
)

type LifecycleCacheItem interface {
	IsExpired() bool
	SetEndTime(time.Time)
}

type lifecycleCacheMetrics struct {
	Evictions uint64
}

type lifecycleCache[T LifecycleCacheItem] struct {
	running cmap.ConcurrentMap[string, T]
	onEvict func(ctx context.Context, instance T)

	metrics lifecycleCacheMetrics
}

func newLifecycleCache[T LifecycleCacheItem]() *lifecycleCache[T] {
	return &lifecycleCache[T]{
		running: cmap.New[T](),
		onEvict: func(ctx context.Context, instance T) {},
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
			items := c.running.Items()

			for key, value := range items {
				if value.IsExpired() {
					c.running.RemoveCb(key, func(key string, value T, exist bool) bool {
						if !exist {
							return false
						}

						go c.onEvict(ctx, value)

						return true
					})

					c.metrics.Evictions += 1
				}
			}
		}
	}
}

func (c *lifecycleCache[T]) SetIfAbsent(key string, value T) bool {
	return c.running.SetIfAbsent(key, value)
}

func (c *lifecycleCache[T]) Get(key string) (T, bool) {
	var zero T
	value, ok := c.running.Get(key)
	if !ok {
		return zero, false
	}

	if value.IsExpired() {
		return zero, false
	}

	return value, true
}

func (c *lifecycleCache[T]) GetAndRemove(key string) (T, bool) {
	var zero T
	v, ok := c.running.Get(key)
	if !ok {
		return zero, false
	}

	// Set end time to now and trigger the eviction.
	// Not removing from the cache, let the eviction handle it.
	v.SetEndTime(time.Now())

	return v, true
}

func (c *lifecycleCache[T]) Remove(key string) bool {
	_, ok := c.GetAndRemove(key)
	return ok
}

func (c *lifecycleCache[T]) Items() (items []T) {
	for _, item := range c.running.Items() {
		if item.IsExpired() {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (c *lifecycleCache[T]) Len() int {
	return c.running.Count()
}

package instance

import (
	"context"
	"time"

	cmap "github.com/orcaman/concurrent-map/v2"
)

type lifecycleCacheMetrics struct {
	Evictions uint64
}

type lifecycleCache struct {
	running cmap.ConcurrentMap[string, *InstanceInfo]
	onEvict func(ctx context.Context, instance *InstanceInfo)

	metrics lifecycleCacheMetrics
}

func newLifecycleCache() *lifecycleCache {
	return &lifecycleCache{
		running: cmap.New[*InstanceInfo](),
		onEvict: func(ctx context.Context, instance *InstanceInfo) {},
		metrics: lifecycleCacheMetrics{
			Evictions: 0,
		},
	}
}

func (c *lifecycleCache) OnEviction(onEvict func(ctx context.Context, instance *InstanceInfo)) {
	c.onEvict = onEvict
}

func (c *lifecycleCache) Metrics() lifecycleCacheMetrics {
	return c.metrics
}

func (c *lifecycleCache) Start(ctx context.Context) {
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
					c.running.RemoveCb(key, func(key string, value *InstanceInfo, exist bool) bool {
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

func (c *lifecycleCache) SetIfAbsent(key string, value *InstanceInfo) bool {
	return c.running.SetIfAbsent(key, value)
}

func (c *lifecycleCache) Get(key string) (*InstanceInfo, bool) {
	value, ok := c.running.Get(key)
	if !ok {
		return nil, false
	}

	if value.IsExpired() {
		return nil, false
	}

	return value, true
}

func (c *lifecycleCache) GetAndRemove(key string) (*InstanceInfo, bool) {
	v, ok := c.running.Get(key)
	if !ok {
		return nil, false
	}

	// Set end time to now and trigger the eviction.
	// Not removing from the cache, let the eviction handle it.
	v.SetEndTime(time.Now())

	return v, true
}

func (c *lifecycleCache) Remove(key string) bool {
	_, ok := c.GetAndRemove(key)
	return ok
}

func (c *lifecycleCache) Items() (items []*InstanceInfo) {
	for _, item := range c.running.Items() {
		if item.IsExpired() {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (c *lifecycleCache) Len() int {
	return c.running.Count()
}

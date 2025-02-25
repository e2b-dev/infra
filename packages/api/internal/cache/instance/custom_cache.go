package instance

import (
	"context"
	"time"

	cmap "github.com/orcaman/concurrent-map/v2"
)

type lifecycleCache struct {
	running cmap.ConcurrentMap[string, *InstanceInfo]
	onEvict func(instance *InstanceInfo)
}

func newLifecycleCache(onEvict func(instance *InstanceInfo)) *lifecycleCache {
	return &lifecycleCache{
		running: cmap.New[*InstanceInfo](),
		onEvict: onEvict,
	}
}

func (c *lifecycleCache) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Get all items from cache before iterating over them
		// to avoid holding the lock while removing items from the cache.
		items := c.running.Items()

		for key, value := range items {
			if value.IsExpired() {
				c.running.RemoveCb(key, func(key string, value *InstanceInfo, exits bool) bool {
					if !exits {
						return false
					}

					go c.onEvict(value)

					return true
				})
			}
		}
	}
}

func (c *lifecycleCache) Set(key string, value *InstanceInfo) {
	c.running.Set(key, value)
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

func (c *lifecycleCache) Remove(key string) bool {
	v, ok := c.running.Get(key)
	if !ok {
		return false
	}

	// Set end time to now trigger eviction.
	v.SetEndTime(time.Now())

	return true
}

func (c *lifecycleCache) Items() (items []*InstanceInfo) {
	for _, item := range c.running.Items() {
		items = append(items, item)
	}
}

package instance

import (
	"context"
	"time"
)

// TODO: this should be removed once we have a better way to handle node sync
// Don't remove instances that were started in the grace period on node sync
// This is to prevent remove instances that are still being started
const syncSandboxRemoveGracePeriod = 10 * time.Second

func (c *MemoryStore) Sync(ctx context.Context, instances []Data, nodeID string) {
	instanceMap := make(map[string]Data)

	// Use a map for faster lookup
	for _, instance := range instances {
		instanceMap[instance.SandboxID] = instance
	}

	// Remove instances that are not in Orchestrator anymore
	for _, item := range c.items.Items() {
		data := item.Data()
		if data.IsExpired() {
			continue
		}

		if data.NodeID != nodeID {
			continue
		}

		if time.Since(data.StartTime) <= syncSandboxRemoveGracePeriod {
			continue
		}

		_, found := instanceMap[data.SandboxID]
		if !found {
			item.SetExpired()
		}
	}

	// Add instances that are not in the cache with the default TTL
	for _, instance := range instances {
		if c.Exists(instance.SandboxID) {
			continue
		}

		c.Add(ctx, instance, false)
	}
}

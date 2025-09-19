package instance

import (
	"context"
	"time"
)

// TODO: this should be removed once we have a better way to handle node sync
// Don't remove instances that were started in the grace period on node sync
// This is to prevent remove instances that are still being started
const syncSandboxRemoveGracePeriod = 10 * time.Second

func (c *MemoryStore) Sync(ctx context.Context, instances []*InstanceInfo, nodeID string) {
	instanceMap := make(map[string]*InstanceInfo)

	// Use a map for faster lookup
	for _, instance := range instances {
		instanceMap[instance.data.SandboxID] = instance
	}

	// Remove instances that are not in Orchestrator anymore
	for _, item := range c.items.Items() {
		if item.data.IsExpired() {
			continue
		}

		if item.data.NodeID != nodeID {
			continue
		}

		if time.Since(item.data.StartTime) <= syncSandboxRemoveGracePeriod {
			continue
		}

		_, found := instanceMap[item.data.SandboxID]
		if !found {
			item.SetExpired()
		}
	}

	// Add instances that are not in the cache with the default TTL
	for _, instance := range instances {
		if c.Exists(instance.data.SandboxID) {
			continue
		}
		c.Add(ctx, instance, false)
	}
}

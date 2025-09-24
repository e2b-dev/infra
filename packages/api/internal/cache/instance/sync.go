package instance

import (
	"context"
	"time"
)

// TODO: this should be removed once we have a better way to handle node sync
// Don't remove sandboxes that were started in the grace period on node sync
// This is to prevent remove instances that are still being started
const syncSandboxRemoveGracePeriod = 10 * time.Second

func (ms *MemoryStore) Sync(ctx context.Context, sandboxes []Sandbox, nodeID string) {
	sandboxMap := make(map[string]Sandbox)

	// Use a map for faster lookup
	for _, sandbox := range sandboxes {
		sandboxMap[sandbox.SandboxID] = sandbox
	}

	// Remove sandboxes that are not in Orchestrator anymore
	for _, item := range ms.items.Items() {
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

		_, found := sandboxMap[data.SandboxID]
		if !found {
			item.SetExpired()
		}
	}

	// Add sandboxes that are not in the cache with the default TTL
	for _, sandbox := range sandboxes {
		if ms.Exists(sandbox.SandboxID) {
			continue
		}

		ms.Add(ctx, sandbox, false)
	}
}

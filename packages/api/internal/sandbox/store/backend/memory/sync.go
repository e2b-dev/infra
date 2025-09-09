package memory

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
)

// TODO: this should be removed once we have a better way to handle node sync
// Don't remove sandboxes that were started in the grace period on node sync
// This is to prevent remove sandboxes that are still being started
const syncSandboxRemoveGracePeriod = 10 * time.Second

func (c *Backend) Sync(ctx context.Context, sandboxes []*store.Sandbox, nodeID string) {
	sandboxMap := make(map[string]*store.Sandbox)

	// Use a map for faster lookup
	for _, sbx := range sandboxes {
		sandboxMap[sbx.SandboxID] = sbx
	}

	// Remove sandboxes that are not in Orchestrator anymore
	for _, sbx := range c.enrichedItems(ctx, nil) {
		if sbx.base.NodeID != nodeID {
			continue
		}

		if time.Since(sbx.base.StartTime) <= syncSandboxRemoveGracePeriod {
			continue
		}
		_, found := sandboxMap[sbx.base.SandboxID]
		if !found {
			sbx.SetExpired()
		}
	}

	// Add sandboxes that are not in the cache with the default TTL
	for _, sbx := range sandboxes {
		if c.Exists(ctx, sbx.SandboxID) {
			continue
		}
		err := c.Add(ctx, sbx, false)
		if err != nil {
			zap.L().Error("error adding sandbox to store", zap.Error(err))
		}
	}
}

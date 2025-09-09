package store

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

// TODO: this should be removed once we have a better way to handle node sync
// Don't remove sandboxes that were started in the grace period on node sync
// This is to prevent remove sandboxes that are still being started
const syncSandboxRemoveGracePeriod = 10 * time.Second

func getMaxAllowedTTL(now time.Time, startTime time.Time, duration, maxInstanceLength time.Duration) time.Duration {
	timeLeft := maxInstanceLength - now.Sub(startTime)
	if timeLeft <= 0 {
		return 0
	}

	return min(timeLeft, duration)
}

// KeepAliveFor the sandbox's expiration timer.
func (c *MemoryStore) KeepAliveFor(sandboxID string, duration time.Duration, allowShorter bool) (*Sandbox, *api.APIError) {
	sandbox, err := c.Get(sandboxID, false)
	if err != nil {
		return nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("Sandbox '%s' is not running anymore", sandboxID), Err: err}
	}

	now := time.Now()

	endTime := sandbox.GetEndTime()
	if !allowShorter && endTime.After(now.Add(duration)) {
		return sandbox, nil
	}

	if (time.Since(sandbox.StartTime)) > sandbox.MaxInstanceLength {
		sandbox.SetExpired()

		msg := fmt.Sprintf("Sandbox '%s' reached maximal allowed uptime", sandboxID)
		return nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: msg, Err: errors.New(msg)}
	} else {
		maxAllowedTTL := getMaxAllowedTTL(now, sandbox.StartTime, duration, sandbox.MaxInstanceLength)

		newEndTime := now.Add(maxAllowedTTL)
		sandbox.SetEndTime(newEndTime)
	}

	return sandbox, nil
}

func (c *MemoryStore) Sync(ctx context.Context, sandboxes []*Sandbox, nodeID string) {
	sandboxMap := make(map[string]*Sandbox)

	// Use a map for faster lookup
	for _, sandbox := range sandboxes {
		sandboxMap[sandbox.SandboxID] = sandbox
	}

	// Remove sandboxes that are not in Orchestrator anymore
	for _, sandbox := range c.Items(nil) {
		if sandbox.NodeID != nodeID {
			continue
		}

		if time.Since(sandbox.StartTime) <= syncSandboxRemoveGracePeriod {
			continue
		}
		_, found := sandboxMap[sandbox.SandboxID]
		if !found {
			sandbox.SetExpired()
		}
	}

	// Add sandboxes that are not in the cache with the default TTL
	for _, sandbox := range sandboxes {
		if c.Exists(sandbox.SandboxID) {
			continue
		}
		err := c.Add(ctx, sandbox, false)
		if err != nil {
			zap.L().Error("error adding sandbox to store", zap.Error(err))
		}
	}
}

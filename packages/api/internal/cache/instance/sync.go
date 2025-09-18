package instance

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

// TODO: this should be removed once we have a better way to handle node sync
// Don't remove instances that were started in the grace period on node sync
// This is to prevent remove instances that are still being started
const syncSandboxRemoveGracePeriod = 10 * time.Second

func getMaxAllowedTTL(now time.Time, startTime time.Time, duration, maxInstanceLength time.Duration) time.Duration {
	timeLeft := maxInstanceLength - now.Sub(startTime)
	if timeLeft <= 0 {
		return 0
	}

	return min(timeLeft, duration)
}

// KeepAliveFor the instance's expiration timer.
func (c *MemoryStore) KeepAliveFor(instanceID string, duration time.Duration, allowShorter bool) (Data, *api.APIError) {
	instance, err := c.Get(instanceID, false)
	if err != nil {
		return Data{}, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("Sandbox '%s' is not running anymore", instanceID), Err: err}
	}

	now := time.Now()

	instance.mu.Lock()
	defer instance.mu.Unlock()

	endTime := instance.data.EndTime
	if !allowShorter && endTime.After(now.Add(duration)) {
		return instance.data, nil
	}

	maxAllowedTTL := getMaxAllowedTTL(now, instance.data.StartTime, duration, instance.data.MaxInstanceLength)
	instance.data.EndTime = now.Add(maxAllowedTTL)
	zap.L().Debug("sandbox ttl updated", zap.String("sandboxID", instance.data.SandboxID), zap.Duration("ttl", maxAllowedTTL))

	return instance.data, nil
}

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

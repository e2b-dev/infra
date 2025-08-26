package instance

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
func (c *InstanceCache) KeepAliveFor(sandbox *InstanceInfo, duration time.Duration, allowShorter bool) (*InstanceInfo, *api.APIError) {
	now := time.Now()

	endTime := sandbox.EndTime
	if !allowShorter && endTime.After(now.Add(duration)) {
		return sandbox, nil
	}

	if (time.Since(sandbox.StartTime)) > sandbox.MaxInstanceLength {
		c.cache.Remove(sandbox.SandboxID)

		msg := fmt.Sprintf("Sandbox '%s' reached maximal allowed uptime", sandbox.SandboxID)
		return nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: msg, Err: errors.New(msg)}
	} else {
		maxAllowedTTL := getMaxAllowedTTL(now, sandbox.StartTime, duration, sandbox.MaxInstanceLength)

		newEndTime := now.Add(maxAllowedTTL)
		sandbox.EndTime = newEndTime
	}

	return sandbox, nil
}

func (c *InstanceCache) Sync(ctx context.Context, instances []*InstanceInfo, nodeID string) {
	instanceMap := make(map[string]*InstanceInfo)

	// Use a map for faster lookup
	for _, instance := range instances {
		instanceMap[instance.SandboxID] = instance
	}

	// Delete instances that are not in Orchestrator anymore
	for _, item := range c.cache.Items() {
		c.checkInstance(instanceMap, nodeID, item)
	}

	// Add instances that are not in the cache with the default TTL
	for _, instance := range instances {
		c.loadInstance(ctx, instance)
	}
}

func (c *InstanceCache) loadInstance(ctx context.Context, instance *InstanceInfo) {
	instance.Lock()
	defer instance.Unlock()

	if c.Exists(instance.SandboxID) {
		return
	}

	err := c.Add(ctx, instance, false)
	if err != nil {
		zap.L().Error("error adding instance to cache", zap.Error(err))
	}
}

func (c *InstanceCache) checkInstance(instanceMap map[string]*InstanceInfo, nodeID string, instance *InstanceInfo) {
	instance.Lock()
	defer instance.Unlock()

	if instance.NodeID != nodeID {
		return
	}
	if time.Since(instance.StartTime) <= syncSandboxRemoveGracePeriod {
		return
	}
	_, found := instanceMap[instance.SandboxID]
	if !found {
		c.cache.Remove(instance.SandboxID)
	}
}

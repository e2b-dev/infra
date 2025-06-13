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
// Don't sync instances that were started in the grace period on node sync
// This is to prevent add/remove instances that are still being started
const syncSandboxGracePeriod = 10 * time.Second

func getMaxAllowedTTL(now time.Time, startTime time.Time, duration, maxInstanceLength time.Duration) time.Duration {
	timeLeft := maxInstanceLength - now.Sub(startTime)
	if timeLeft <= 0 {
		return 0
	}

	return min(timeLeft, duration)
}

// KeepAliveFor the instance's expiration timer.
func (c *InstanceCache) KeepAliveFor(instanceID string, duration time.Duration, allowShorter bool) (*InstanceInfo, *api.APIError) {
	instance, err := c.Get(instanceID)
	if err != nil {
		return nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("Sandbox '%s' not found", instanceID), Err: err}
	}

	now := time.Now()

	endTime := instance.GetEndTime()
	if !allowShorter && endTime.After(now.Add(duration)) {
		return instance, nil
	}

	if (time.Since(instance.StartTime)) > instance.MaxInstanceLength {
		c.cache.Remove(instanceID)

		msg := fmt.Sprintf("Sandbox '%s' reached maximal allowed uptime", instanceID)
		return nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: msg, Err: errors.New(msg)}
	} else {
		maxAllowedTTL := getMaxAllowedTTL(now, instance.StartTime, duration, instance.MaxInstanceLength)

		newEndTime := now.Add(maxAllowedTTL)
		instance.SetEndTime(newEndTime)
	}

	return instance, nil
}

func (c *InstanceCache) Sync(ctx context.Context, instances []*InstanceInfo, nodeID string) {
	instanceMap := make(map[string]*InstanceInfo)

	// Use a map for faster lookup
	for _, instance := range instances {
		instanceMap[instance.Instance.SandboxID] = instance
	}

	// Delete instances that are not in Orchestrator anymore
	for _, item := range c.cache.Items() {
		if item.Instance.ClientID != nodeID {
			continue
		}
		if time.Since(item.StartTime) <= syncSandboxGracePeriod {
			continue
		}
		_, found := instanceMap[item.Instance.SandboxID]
		if !found {
			c.cache.Remove(item.Instance.SandboxID)
		}
	}

	// Add instances that are not in the cache with the default TTL
	for _, instance := range instances {
		if time.Since(instance.StartTime) <= syncSandboxGracePeriod {
			continue
		}
		if c.Exists(instance.Instance.SandboxID) {
			continue
		}
		err := c.Add(ctx, instance, false)
		if err != nil {
			zap.L().Error("error adding instance to cache", zap.Error(err))
		}
	}
}

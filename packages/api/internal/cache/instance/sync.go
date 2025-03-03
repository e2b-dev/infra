package instance

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
)

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
		return nil, &api.APIError{Code: http.StatusNotFound, ClientMsg: fmt.Sprintf("sandbox \"%s\" not found", instanceID), Err: err}
	}

	now := time.Now()

	endTime := instance.GetEndTime()
	if !allowShorter && endTime.After(now.Add(duration)) {
		return instance, nil
	}

	if (time.Since(instance.StartTime)) > instance.MaxInstanceLength {
		c.cache.Remove(instanceID)

		msg := fmt.Sprintf("sandbox \"%s\" reached maximal allowed uptime", instanceID)
		return nil, &api.APIError{Code: http.StatusForbidden, ClientMsg: msg, Err: fmt.Errorf(msg)}
	} else {
		maxAllowedTTL := getMaxAllowedTTL(now, instance.StartTime, duration, instance.MaxInstanceLength)

		newEndTime := now.Add(maxAllowedTTL)
		instance.SetEndTime(newEndTime)
	}

	return instance, nil
}

func (c *InstanceCache) Sync(instances []*InstanceInfo, nodeID string) {
	instanceMap := make(map[string]*InstanceInfo)

	// Use map for faster lookup
	for _, instance := range instances {
		instanceMap[instance.Instance.SandboxID] = instance
	}

	// Delete instances that are not in Orchestrator anymore
	for _, item := range c.cache.Items() {
		if item.Instance.ClientID != nodeID {
			continue
		}
		_, found := instanceMap[item.Instance.SandboxID]
		if !found {
			c.cache.Remove(item.Instance.SandboxID)
		}
	}

	// Add instances that are not in the cache with the default TTL
	for _, instance := range instances {
		if !c.Exists(instance.Instance.SandboxID) {
			err := c.Add(instance, false)
			if err != nil {
				fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
			}
		}
	}

	// Send running instances event to analytics
	instanceIds := make([]string, len(instances))
	for i, instance := range instances {
		instanceIds[i] = instance.Instance.SandboxID
	}

	go func() {
		_, err := c.analytics.RunningInstances(context.Background(), &analyticscollector.RunningInstancesEvent{InstanceIds: instanceIds, Timestamp: timestamppb.Now()})
		if err != nil {
			c.logger.Errorf("Error sending running instances event to analytics\n: %v", err)
		}
	}()
}

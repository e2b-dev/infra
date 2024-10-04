package instance

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/analytics"
)

func getMaxAllowedTTL(now time.Time, startTime time.Time, duration, maxInstanceLength time.Duration) time.Duration {
	timeLeft := maxInstanceLength - now.Sub(startTime)
	if timeLeft <= 0 {
		return 0
	}

	return min(timeLeft, duration)
}

// KeepAliveFor the instance's expiration timer.
func (c *InstanceCache) KeepAliveFor(instanceID string, duration time.Duration, allowShorter bool) (*InstanceInfo, error) {
	item, err := c.Get(instanceID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	instance := item.Value()
	if !allowShorter && instance.EndTime.After(now.Add(duration)) {
		return &instance, nil
	}

	if (time.Since(instance.StartTime)) > instance.MaxInstanceLength {
		c.cache.Delete(instanceID)

		return nil, fmt.Errorf("instance \"%s\" reached maximal allowed uptime", instanceID)
	} else {
		maxAllowedTTL := getMaxAllowedTTL(now, instance.StartTime, duration, instance.MaxInstanceLength)

		newEndTime := now.Add(maxAllowedTTL)
		instance.EndTime = newEndTime

		item = c.cache.Set(instanceID, instance, maxAllowedTTL)
		if item == nil {
			return nil, fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
		}
	}

	return &instance, nil
}

func (c *InstanceCache) Sync(instances []*InstanceInfo, nodeID string) {
	instanceMap := make(map[string]*InstanceInfo)

	// Use map for faster lookup
	for _, instance := range instances {
		instanceMap[instance.Instance.SandboxID] = instance
	}

	// Delete instances that are not in Orchestrator anymore
	for _, item := range c.cache.Items() {
		if item.Value().Instance.ClientID == nodeID {
			_, found := instanceMap[item.Key()]
			if !found {
				c.cache.Delete(item.Key())
			}
		}
	}

	// Add instances that are not in the cache with the default TTL
	for _, instance := range instances {
		err := c.Add(*instance)
		if err != nil {
			fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
		}
	}
}

func (c *InstanceCache) SendAnalyticsEvent() {
	// Send running instances event to analytics
	instanceIds := make([]string, c.cache.Len())
	for i, sbxID := range c.cache.Keys() {
		instanceIds[i] = sbxID
	}

	_, err := c.analytics.RunningInstances(context.Background(), &analytics.RunningInstancesEvent{InstanceIds: instanceIds, Timestamp: timestamppb.Now()})
	if err != nil {
		c.logger.Errorf("Error sending running instances event to analytics\n: %v", err)
	}
}

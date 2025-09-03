package instance

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.uber.org/zap"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

type RemoveType string

const (
	RemoveTypePause RemoveType = "pause"
	RemoveTypeKill  RemoveType = "kill"
)

// Add the instance to the cache
func (c *MemoryStore) Add(ctx context.Context, instance *InstanceInfo, newlyCreated bool) error {
	sbxlogger.I(instance).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", instance.StartTime),
		zap.Time("end_time", instance.GetEndTime()),
	)

	if instance.SandboxID == "" {
		return fmt.Errorf("instance is missing sandbox ID")
	}

	if instance.TeamID == uuid.Nil {
		return fmt.Errorf("instance %s is missing team ID", instance.SandboxID)
	}

	if instance.ClientID == "" {
		return fmt.Errorf("instance %s is missing client ID", instance.ClientID)
	}

	if instance.TemplateID == "" {
		return fmt.Errorf("instance %s is missing env ID", instance.TemplateID)
	}

	endTime := instance.GetEndTime()

	if instance.StartTime.IsZero() || endTime.IsZero() || instance.StartTime.After(endTime) {
		return fmt.Errorf("instance %s has invalid start(%s)/end(%s) times", instance.SandboxID, instance.StartTime, endTime)
	}

	if endTime.Sub(instance.StartTime) > instance.MaxInstanceLength {
		instance.SetEndTime(instance.StartTime.Add(instance.MaxInstanceLength))
	}

	c.set(ctx, instance.SandboxID, instance, newlyCreated)
	c.updateCounters(ctx, instance, 1, newlyCreated)

	// Release the reservation if it exists
	c.reservations.release(instance.SandboxID)

	return nil
}

// Exists Check if the instance exists in the cache or is being evicted.
func (c *MemoryStore) Exists(instanceID string) bool {
	return c.items.Has(instanceID)
}

// Get the item from the cache.
func (c *MemoryStore) Get(instanceID string, includeEvicting bool) (*InstanceInfo, error) {
	item, ok := c.items.Get(instanceID)
	if !ok {
		return nil, fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	}

	if item.IsExpired() && !includeEvicting {
		return nil, fmt.Errorf("instance \"%s\" is being evicted", instanceID)
	}

	return item, nil
}

// Remove the instance from the cache (no eviction callback).
func (c *MemoryStore) Remove(ctx context.Context, instanceID string, removeType RemoveType) error {
	sbx, ok := c.items.Get(instanceID)
	if !ok {
		return fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	}

	acquired := sbx.stopLock.TryLock()
	if !acquired {
		// TODO: return a typed error
		return fmt.Errorf("instance \"%s\" is already being removed", instanceID)
	}
	defer sbx.stopLock.Unlock()

	// Mark the stop time
	if !sbx.IsExpired() {
		sbx.SetExpired()
	}

	sbx.mu.Lock()
	sbx.state = StateShuttingDown
	sbx.mu.Unlock()

	err := c.deleteInstance(ctx, sbx, removeType)
	sbx.stopDone(err, removeType)
	if err != nil {
		return fmt.Errorf("error removing instance \"%s\": %w", instanceID, err)
	}

	c.items.Remove(instanceID)

	return nil
}

func (c *MemoryStore) Items(teamID *uuid.UUID) []*InstanceInfo {
	items := make([]*InstanceInfo, 0)
	for _, item := range c.items.Items() {
		if item.IsExpired() {
			continue
		}
		if teamID == nil || item.TeamID == *teamID {
			items = append(items, item)
		}

		items = append(items, item)
	}

	return items
}

func (c *MemoryStore) ItemsByState(teamID *uuid.UUID, states []State) map[State][]*InstanceInfo {
	items := make(map[State][]*InstanceInfo)
	for _, item := range c.items.Items() {
		if teamID != nil && item.TeamID != *teamID {
			continue
		}

		if slices.Contains(states, item.state) {
			if _, ok := items[item.state]; !ok {
				items[item.state] = []*InstanceInfo{}
			}

			items[item.state] = append(items[item.state], item)
		}
	}

	return items
}

func (c *MemoryStore) Len(teamID *uuid.UUID) int {
	return len(c.Items(teamID))
}

func (c *MemoryStore) set(ctx context.Context, key string, value *InstanceInfo, created bool) {
	inserted := c.items.SetIfAbsent(key, value)
	if inserted {
		// Run asynchronously to avoid blocking the main flow
		go func() {
			c.insertAnalytics(ctx, value, created)
		}()
	}
}

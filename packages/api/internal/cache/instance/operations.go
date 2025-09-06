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
func (c *MemoryStore) Add(ctx context.Context, sandbox *InstanceInfo, newlyCreated bool) error {
	sbxlogger.I(sandbox).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.StartTime),
		zap.Time("end_time", sandbox.GetEndTime()),
	)

	if sandbox.SandboxID == "" {
		return fmt.Errorf("sandbox is missing sandbox ID")
	}

	if sandbox.TeamID == uuid.Nil {
		return fmt.Errorf("sandbox %s is missing team ID", sandbox.SandboxID)
	}

	if sandbox.ClientID == "" {
		return fmt.Errorf("sandbox %s is missing client ID", sandbox.ClientID)
	}

	if sandbox.TemplateID == "" {
		return fmt.Errorf("sandbox %s is missing env ID", sandbox.TemplateID)
	}

	endTime := sandbox.GetEndTime()

	if sandbox.StartTime.IsZero() || endTime.IsZero() || sandbox.StartTime.After(endTime) {
		return fmt.Errorf("sandbox %s has invalid start(%s)/end(%s) times", sandbox.SandboxID, sandbox.StartTime, endTime)
	}

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.SetEndTime(sandbox.StartTime.Add(sandbox.MaxInstanceLength))
	}

	c.items.SetIfAbsent(sandbox.SandboxID, sandbox)

	for _, callback := range c.insertCallbacks {
		callback(ctx, sandbox, newlyCreated)
	}

	for _, callback := range c.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, newlyCreated)
	}
	// Release the reservation if it exists
	c.reservations.release(sandbox.SandboxID)

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
func (c *MemoryStore) Remove(ctx context.Context, instanceID string, removeType RemoveType) (err error) {
	sbx, ok := c.items.Get(instanceID)
	if !ok {
		return fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	}

	// Makes sure there's only one removal
	err = sbx.markRemoving(removeType)
	if err != nil {
		return err
	}

	// Remove from the cache
	defer c.items.Remove(instanceID)

	// Remove the sandbox from the node
	err = c.removeSandbox(ctx, sbx, removeType)
	for _, callback := range c.removeAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sbx, removeType)
	}
	sbx.stopDone(err)
	if err != nil {
		return fmt.Errorf("error removing instance \"%s\": %w", instanceID, err)
	}

	return nil
}

func (c *MemoryStore) Items(teamID *uuid.UUID) []*InstanceInfo {
	items := make([]*InstanceInfo, 0)
	for _, item := range c.items.Items() {
		if item.IsExpired() {
			continue
		}

		if teamID != nil && item.TeamID != *teamID {
			continue
		}

		items = append(items, item)
	}

	return items
}

func (c *MemoryStore) ExpiredItems() []*InstanceInfo {
	items := make([]*InstanceInfo, 0)
	for _, item := range c.items.Items() {
		if !item.IsExpired() {
			continue
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

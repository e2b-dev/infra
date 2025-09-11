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
func (c *MemoryStore) Add(ctx context.Context, sandbox *InstanceInfo, newlyCreated bool) {
	sandbox.dataMu.Lock()
	defer sandbox.dataMu.Unlock()

	sbxlogger.I(sandbox.data).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.data.StartTime),
		zap.Time("end_time", sandbox.data.EndTime),
	)

	endTime := sandbox.data.EndTime

	if endTime.Sub(sandbox.data.StartTime) > sandbox.data.MaxInstanceLength {
		sandbox.data.EndTime = sandbox.data.StartTime.Add(sandbox.data.MaxInstanceLength)
	}

	c.items.SetIfAbsent(sandbox.data.SandboxID, sandbox)

	for _, callback := range c.insertCallbacks {
		callback(ctx, sandbox.data, newlyCreated)
	}

	for _, callback := range c.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox.data, newlyCreated)
	}
	// Release the reservation if it exists
	c.reservations.release(sandbox.data.SandboxID)
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

	if item.data.IsExpired() && !includeEvicting {
		return nil, fmt.Errorf("instance \"%s\" is being evicted", instanceID)
	}

	return item, nil
}

func (c *MemoryStore) Remove(instanceID string) {
	c.items.Remove(instanceID)
}

func (c *MemoryStore) Items(teamID *uuid.UUID) []Data {
	items := make([]Data, 0)
	for _, item := range c.items.Items() {
		data := item.data
		if data.IsExpired() {
			continue
		}

		if teamID != nil && data.TeamID != *teamID {
			continue
		}

		items = append(items, data)
	}

	return items
}

func (c *MemoryStore) ItemsToEvict() []*InstanceInfo {
	items := make([]*InstanceInfo, 0)
	for _, item := range c.items.Items() {
		if !item.data.IsExpired() {
			continue
		}

		if item.data.State != StateRunning {
			continue
		}

		items = append(items, item)
	}

	return items
}

func (c *MemoryStore) ItemsByState(teamID *uuid.UUID, states []State) map[State][]Data {
	items := make(map[State][]Data)
	for _, item := range c.items.Items() {
		if teamID != nil && item.data.TeamID != *teamID {
			continue
		}

		data := item.data
		if slices.Contains(states, data.State) {
			if _, ok := items[data.State]; !ok {
				items[data.State] = []Data{}
			}

			items[data.State] = append(items[data.State], data)
		}
	}

	return items
}

func (c *MemoryStore) Len(teamID *uuid.UUID) int {
	return len(c.Items(teamID))
}

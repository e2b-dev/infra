package instance

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

type StateAction string

const (
	StateActionPause StateAction = "pause"
	StateActionKill  StateAction = "kill"
)

// Add the instance to the cache
func (c *MemoryStore) Add(ctx context.Context, sandbox Data, newlyCreated bool) {
	sbxlogger.I(sandbox).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", sandbox.StartTime),
		zap.Time("end_time", sandbox.EndTime),
	)

	endTime := sandbox.EndTime

	if endTime.Sub(sandbox.StartTime) > sandbox.MaxInstanceLength {
		sandbox.EndTime = sandbox.StartTime.Add(sandbox.MaxInstanceLength)
	}

	added := c.items.SetIfAbsent(sandbox.SandboxID, NewInstanceInfo(sandbox))
	if !added {
		zap.L().Warn("Sandbox already exists in cache", logger.WithSandboxID(sandbox.SandboxID))
		return
	}

	for _, callback := range c.insertCallbacks {
		callback(ctx, sandbox, newlyCreated)
	}

	for _, callback := range c.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, newlyCreated)
	}
	// Release the reservation if it exists
	c.reservations.release(sandbox.SandboxID)
}

// Exists Check if the instance exists in the cache or is being evicted.
func (c *MemoryStore) Exists(sandboxID string) bool {
	return c.items.Has(sandboxID)
}

// Get the item from the cache.
func (c *MemoryStore) Get(sandboxID string) (*InstanceInfo, error) {
	item, ok := c.items.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	return item, nil
}

// GetData the item from the cache.
func (c *MemoryStore) GetData(sandboxID string, includeEvicting bool) (Data, error) {
	item, ok := c.items.Get(sandboxID)
	if !ok {
		return Data{}, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	data := item.Data()

	if data.IsExpired() && !includeEvicting {
		return Data{}, fmt.Errorf("sandbox \"%s\" is being evicted", sandboxID)
	}

	return data, nil
}

func (c *MemoryStore) Remove(sandboxID string) {
	c.items.Remove(sandboxID)
}

func (c *MemoryStore) Items(teamID *uuid.UUID) []Data {
	items := make([]Data, 0)
	for _, item := range c.items.Items() {
		data := item.Data()
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

func (c *MemoryStore) ItemsToEvict() []Data {
	items := make([]Data, 0)
	for _, item := range c.items.Items() {
		data := item.Data()
		if !data.IsExpired() {
			continue
		}

		if data.State != StateRunning {
			continue
		}

		items = append(items, data)
	}

	return items
}

func (c *MemoryStore) ItemsByState(teamID *uuid.UUID, states []State) map[State][]Data {
	items := make(map[State][]Data)
	for _, item := range c.items.Items() {
		data := item.Data()
		if teamID != nil && data.TeamID != *teamID {
			continue
		}

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

func (c *MemoryStore) ExtendEndTime(sandboxID string, newEndTime time.Time, allowShorter bool) (bool, error) {
	item, ok := c.items.Get(sandboxID)
	if !ok {
		return false, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	return item.ExtendEndTime(newEndTime, allowShorter), nil
}

func (c *MemoryStore) StartRemoving(ctx context.Context, sandboxID string, stateAction StateAction) (alreadyDone bool, callback func(error), err error) {
	sbx, err := c.Get(sandboxID)
	if err != nil {
		return false, nil, err
	}

	return sbx.startRemoving(ctx, stateAction)
}

package memory

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
)

func (c *Backend) Add(ctx context.Context, sandbox *store.Sandbox, newlyCreated bool) error {
	sbx := newSandbox(sandbox)
	c.items.SetIfAbsent(sandbox.SandboxID, sbx)

	// Release the reservation if it exists
	c.reservations.release(sandbox.SandboxID)

	return nil
}

// Exists Check if the sandbox exists in the cache or is being evicted.
func (c *Backend) Exists(ctx context.Context, sandboxID string) bool {
	return c.items.Has(sandboxID)
}

// Get the item from the cache.
func (c *Backend) Get(ctx context.Context, sandboxID string, includeEvicting bool) (*store.Sandbox, error) {
	item, err := c.get(ctx, sandboxID, includeEvicting)
	if err != nil {
		return nil, err
	}

	return item.Base(), nil
}

// Get the item from the cache.
func (c *Backend) get(ctx context.Context, sandboxID string, includeEvicting bool) (*sandbox, error) {
	item, ok := c.items.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	if item.IsExpired() && !includeEvicting {
		return nil, fmt.Errorf("sandbox \"%s\" is being evicted", sandboxID)
	}

	return item, nil
}

func (c *Backend) Remove(ctx context.Context, sandboxID string, removeType store.RemoveType) (err error) {
	sbx, ok := c.items.Get(sandboxID)
	if !ok {
		return fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	// Remove from the cache
	defer c.items.Remove(sandboxID)

	sbx.stopDone(err)
	if err != nil {
		return fmt.Errorf("error removing sandbox \"%s\": %w", sandboxID, err)
	}

	return nil
}

func (c *Backend) MarkRemoving(ctx context.Context, sandboxID string, removeType store.RemoveType) (*store.Sandbox, error) {
	sbx, ok := c.items.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}

	// Makes sure there's only one removal
	err := sbx.markRemoving(removeType)
	if err != nil {
		return nil, err
	}

	return sbx.Base(), nil
}

func (c *Backend) Items(ctx context.Context, teamID *uuid.UUID) []*store.Sandbox {
	items := make([]*store.Sandbox, 0)
	for _, item := range c.items.Items() {
		if item.IsExpired() {
			continue
		}

		if teamID != nil && item.base.TeamID != *teamID {
			continue
		}

		items = append(items, item.Base())
	}

	return items
}

func (c *Backend) enrichedItems(ctx context.Context, teamID *uuid.UUID) []*sandbox {
	items := make([]*sandbox, 0)
	for _, item := range c.items.Items() {
		if item.IsExpired() {
			continue
		}

		if teamID != nil && item.base.TeamID != *teamID {
			continue
		}

		items = append(items, item)
	}

	return items
}

func (c *Backend) ExpiredItems(ctx context.Context) []*store.Sandbox {
	items := make([]*store.Sandbox, 0)
	for _, item := range c.items.Items() {
		if !item.IsExpired() {
			continue
		}
		items = append(items, item.Base())
	}

	return items
}

func (c *Backend) ItemsByState(ctx context.Context, teamID *uuid.UUID, states []store.State) map[store.State][]*store.Sandbox {
	items := make(map[store.State][]*store.Sandbox)
	for _, item := range c.items.Items() {
		if teamID != nil && item.base.TeamID != *teamID {
			continue
		}

		if slices.Contains(states, item.base.State) {
			if _, ok := items[item.base.State]; !ok {
				items[item.base.State] = []*store.Sandbox{}
			}

			items[item.base.State] = append(items[item.base.State], item.Base())
		}
	}

	return items
}

func (c *Backend) Len(ctx context.Context, teamID *uuid.UUID) int {
	return len(c.Items(ctx, teamID))
}

func (c *Backend) Update(ctx context.Context, sandbox *store.Sandbox) error {
	c.items.Set(sandbox.SandboxID, newSandbox(sandbox))

	return nil
}

func (c *Backend) WaitForStop(ctx context.Context, sandboxID string) error {
	item, err := c.get(ctx, sandboxID, true)
	if err != nil {
		return err
	}

	return item.WaitForStop(ctx)
}

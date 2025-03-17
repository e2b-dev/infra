package instance

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
)

func (c *InstanceCache) Count() int {
	return c.cache.Len()
}

func (c *InstanceCache) CountForTeam(teamID uuid.UUID) (count uint) {
	for _, item := range c.cache.Items() {
		currentTeamID := item.TeamID

		if currentTeamID == nil {
			continue
		}

		if *currentTeamID == teamID {
			count++
		}
	}

	return count
}

// Exists Check if the instance exists in the cache or is being evicted.
func (c *InstanceCache) Exists(instanceID string) bool {
	return c.cache.Has(instanceID, true)
}

// Get the item from the cache.
func (c *InstanceCache) Get(instanceID string) (*InstanceInfo, error) {
	item, ok := c.cache.Get(instanceID)
	if !ok {
		return nil, fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	}

	return item, nil
}

func (c *InstanceCache) GetInstances(teamID *uuid.UUID) (instances []*InstanceInfo) {
	for _, item := range c.cache.Items() {
		currentTeamID := item.TeamID

		if teamID == nil || *currentTeamID == *teamID {
			instances = append(instances, item)
		}
	}

	return instances
}

// Add the instance to the cache and start expiration timer.
// If the instance already exists we do nothing - it was loaded from Orchestrator.
// TODO: Any error here should delete the sandbox
func (c *InstanceCache) Add(ctx context.Context, instance *InstanceInfo, newlyCreated bool) error {
	sbxlogger.I(instance).Debug("Adding sandbox to cache",
		zap.Bool("newly_created", newlyCreated),
		zap.Time("start_time", instance.StartTime),
		zap.Time("end_time", instance.GetEndTime()),
	)

	if instance.Instance == nil {
		return fmt.Errorf("instance doesn't contain info about inself")
	}

	if instance.Instance.SandboxID == "" {
		return fmt.Errorf("instance is missing sandbox ID")
	}

	if instance.TeamID == nil {
		return fmt.Errorf("instance %s is missing team ID", instance.Instance.SandboxID)
	}

	if instance.Instance.ClientID == "" {
		return fmt.Errorf("instance %s is missing client ID", instance.Instance.ClientID)
	}

	if instance.Instance.TemplateID == "" {
		return fmt.Errorf("instance %s is missing env ID", instance.Instance.TemplateID)
	}

	endTime := instance.GetEndTime()

	if instance.StartTime.IsZero() || endTime.IsZero() || instance.StartTime.After(endTime) {
		return fmt.Errorf("instance %s has invalid start(%s)/end(%s) times", instance.Instance.SandboxID, instance.StartTime, endTime)
	}

	if endTime.Sub(instance.StartTime) > instance.MaxInstanceLength {
		instance.SetEndTime(instance.StartTime.Add(instance.MaxInstanceLength))
	}

	c.Set(instance.Instance.SandboxID, instance)
	c.UpdateCounters(ctx, instance, 1, newlyCreated)

	// Release the reservation if it exists
	c.reservations.release(instance.Instance.SandboxID)

	return nil
}

// Delete the instance and remove it from the cache.
func (c *InstanceCache) Delete(instanceID string, pause bool) bool {
	value, found := c.cache.GetAndRemove(instanceID)
	if found {
		value.AutoPause.Store(pause)

		if pause {
			c.MarkAsPausing(value)
		}
	}

	return found
}

func (c *InstanceCache) Items() []*InstanceInfo {
	return c.cache.Items()
}

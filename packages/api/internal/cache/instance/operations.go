package instance

import (
	"fmt"

	"github.com/google/uuid"
)

func (c *InstanceCache) Count() int {
	return c.instances.Len()
}

func (c *InstanceCache) CountForTeam(teamID uuid.UUID) (count uint) {
	for _, item := range c.instances.Items() {
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

// Exists Check if the instance exists in the cache.
func (c *InstanceCache) Exists(instanceID string) bool {
	_, exists := c.instances.Get(instanceID)

	return exists
}

// Get the item from the cache.
func (c *InstanceCache) Get(instanceID string) (*InstanceInfo, error) {
	item, _ := c.instances.Get(instanceID)
	if item != nil {
		return item, nil
	} else {
		return nil, fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	}
}

// GetInstance from the cache.
func (c *InstanceCache) GetInstance(instanceID string) (*InstanceInfo, error) {
	item, err := c.Get(instanceID)
	if err != nil {
		return nil, fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	} else {
		return item, nil
	}
}

func (c *InstanceCache) GetInstances(teamID *uuid.UUID) (instances []*InstanceInfo) {
	for _, item := range c.instances.Items() {
		currentTeamID := item.TeamID

		if teamID == nil || *currentTeamID == *teamID {
			instances = append(instances, item)
		}
	}

	return instances
}

// Add the instance to the cache and start expiration timer.
// If the instance already exists we do nothing - it was loaded from Orchestrator.
func (c *InstanceCache) Add(instance *InstanceInfo, newlyCreated bool) error {
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

	endTime := instance.endTime

	if instance.StartTime.IsZero() || endTime.IsZero() || instance.StartTime.After(endTime) {
		return fmt.Errorf("instance %s has invalid start(%s)/end(%s) times", instance.Instance.SandboxID, instance.StartTime, endTime)
	}

	if endTime.Sub(instance.StartTime) > instance.MaxInstanceLength {
		instance.SetEndTime(instance.StartTime.Add(instance.MaxInstanceLength))
	}

	c.Set(instance.Instance.SandboxID, instance)
	c.UpdateCounters(instance, 1, newlyCreated)

	// Release the reservation if it exists
	c.reservations.release(instance.Instance.SandboxID)

	return nil
}

// Delete the instance and remove it from the cache.
func (c *InstanceCache) Delete(instanceID string, pause bool) bool {
	value, found := c.instances.GetAndRemove(instanceID)
	if found {
		*value.AutoPause = pause

		if pause {
			v := value
			c.MarkAsPausing(v)
		}
	}

	return found
}

func (c *InstanceCache) Items() []*InstanceInfo {
	return c.instances.Items()
}

package instance

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
)

func (c *InstanceCache) Count() int {
	return c.cache.Len()
}

func (c *InstanceCache) CountForTeam(teamID uuid.UUID) (count uint) {
	for _, item := range c.cache.Items() {
		currentTeamID := item.Value().TeamID

		if currentTeamID == nil {
			continue
		}

		if *currentTeamID == teamID {
			count++
		}
	}

	return count
}

// Check if the instance exists in the cache.
func (c *InstanceCache) Exists(instanceID string) bool {
	item := c.cache.Get(instanceID, ttlcache.WithDisableTouchOnHit[string, InstanceInfo]())

	return item != nil
}

// Get the item from the cache.
func (c *InstanceCache) Get(instanceID string) (*ttlcache.Item[string, InstanceInfo], error) {
	item := c.cache.Get(instanceID, ttlcache.WithDisableTouchOnHit[string, InstanceInfo]())
	if item != nil {
		return item, nil
	} else {
		return nil, fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	}
}

// GetInstance from the cache.
func (c *InstanceCache) GetInstance(instanceID string) (InstanceInfo, error) {
	item, err := c.Get(instanceID)
	if err != nil {
		return InstanceInfo{}, fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	} else {
		return item.Value(), nil
	}
}

func (c *InstanceCache) GetInstances(teamID *uuid.UUID) (instances []InstanceInfo) {
	for _, item := range c.cache.Items() {
		currentTeamID := item.Value().TeamID

		if teamID == nil || *currentTeamID == *teamID {
			instances = append(instances, item.Value())
		}
	}

	return instances
}

// Add the instance to the cache and start expiration timer.
// If the instance already exists we do nothing - it was loaded from Orchestrator.
func (c *InstanceCache) Add(instance InstanceInfo, newlyCreated bool) error {
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

	if instance.StartTime.IsZero() || instance.EndTime.IsZero() || instance.StartTime.After(instance.EndTime) {
		return fmt.Errorf("instance %s has invalid start(%s)/end(%s) times", instance.Instance.SandboxID, instance.StartTime, instance.EndTime)
	}

	if instance.EndTime.Sub(instance.StartTime) > instance.MaxInstanceLength {
		instance.EndTime = instance.StartTime.Add(instance.MaxInstanceLength)
	}

	ttl := instance.EndTime.Sub(time.Now())
	if ttl <= 0 {
		ttl = time.Nanosecond
		// TODO: It would be probably better to return error here, but in that case we need to make sure that sbxs in orchestrator are killed
		// return fmt.Errorf("instance \"%s\" has already expired", instance.Instance.SandboxID)
	}

	c.cache.Set(instance.Instance.SandboxID, instance, ttl)
	c.UpdateCounters(instance, 1, newlyCreated)

	// Release the reservation if it exists
	c.reservations.release(instance.Instance.SandboxID)

	return nil
}

// Delete the instance and remove it from the cache.
func (c *InstanceCache) Delete(instanceID string, pause bool) bool {
	value, found := c.cache.GetAndDelete(instanceID, ttlcache.WithDisableTouchOnHit[string, InstanceInfo]())
	if found {
		*value.Value().AutoPause = pause
	}

	return found
}

func (c *InstanceCache) Items() (infos []InstanceInfo) {
	for _, item := range c.cache.Items() {
		infos = append(infos, item.Value())
	}

	return infos
}

package nomad

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	instanceExpiration = time.Second * 12
	cacheSyncTime      = time.Second * 180
)

type InstanceInfo struct {
	Instance  *api.Instance
	TeamID    *string
	StartTime *time.Time
}

type InstanceCache struct {
	cache   *ttlcache.Cache[string, InstanceInfo]
	counter metric.Int64UpDownCounter
}

// Add the instance to the cache and start expiration timer.
func (c *InstanceCache) Add(instance *api.Instance, teamID *string, startTime *time.Time) error {
	if c.Exists(instance.InstanceID) {
		return fmt.Errorf("instance \"%s\" already exists", instance.InstanceID)
	}

	instanceData := InstanceInfo{
		Instance:  instance,
		TeamID:    teamID,
		StartTime: startTime,
	}

	c.cache.Set(instance.InstanceID, instanceData, ttlcache.DefaultTTL)
	c.counter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("instance_id", instance.InstanceID)))
	return nil
}

// Refresh the instance's expiration timer.
func (c *InstanceCache) Refresh(instanceID string) error {
	item := c.cache.Get(instanceID)

	if item == nil {
		return fmt.Errorf("instance \"%s\" doesn't exist", instanceID)
	}

	return nil
}

// Get the instance from the cache.
func (c *InstanceCache) Get(instanceID string) InstanceInfo {
	item := c.cache.Get(instanceID, ttlcache.WithDisableTouchOnHit[string, InstanceInfo]())
	if item != nil {
		return item.Value()
	} else {
		panic(fmt.Errorf("instance \"%s\" doesn't exist", instanceID))
	}
}

// Check if the instance exists in the cache.
func (c *InstanceCache) Exists(instanceID string) bool {
	item := c.cache.Get(instanceID, ttlcache.WithDisableTouchOnHit[string, InstanceInfo]())

	return item != nil
}

func (c *InstanceCache) Sync(instances []*api.Instance) {
	for _, instance := range instances {
		if !c.Exists(instance.InstanceID) {
			err := c.Add(instance, nil, nil)
			if err != nil {
				fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
			}
		}
	}
}

// We will need to either use Redis for storing active instances OR retrieve them from Nomad when we start API to keep everything in sync
// We are retrieving the tasks from Nomad now.
func NewInstanceCache(deleteInstance func(data InstanceInfo, purge bool) *api.APIError, initialInstances []*api.Instance, counter metric.Int64UpDownCounter) *InstanceCache {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, InstanceInfo](instanceExpiration),
	)

	cache.OnEviction(func(ctx context.Context, er ttlcache.EvictionReason, i *ttlcache.Item[string, InstanceInfo]) {
		if er == ttlcache.EvictionReasonExpired || er == ttlcache.EvictionReasonDeleted {
			err := deleteInstance(i.Value(), true)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error deleting instance (%v)\n: %+v", er, err.Err)
			}
			counter.Add(ctx, -1, metric.WithAttributes(attribute.String("instance_id", i.Value().Instance.InstanceID)))
		}
	})

	instanceCache := &InstanceCache{
		cache:   cache,
		counter: counter,
	}

	for _, instance := range initialInstances {
		err := instanceCache.Add(instance, nil, nil)
		if err != nil {
			fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
		}
	}

	go cache.Start()

	return instanceCache
}

// Sync the cache with the actual instances in Nomad to handle instances that died.
func (c *InstanceCache) KeepInSync(client *NomadClient) {
	for {
		time.Sleep(cacheSyncTime)

		activeInstances, err := client.GetInstances()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading current instances from Nomad\n: %+v", err.Err)
		} else {
			c.Sync(activeInstances)
		}
	}
}

func (c *InstanceCache) Count() int {
	return c.cache.Len()
}

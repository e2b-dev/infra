package edge

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"time"
)

type serviceDiscovery struct {
	redis *redis.Client
	mx    *redsync.Mutex

	serviceId string
}

const (
	statusHealthy   = "healthy"
	statusUnhealthy = "unhealthy"
	statusDraining  = "draining"
)

type serviceDiscoveryItem struct {
	Version string `json:"version"`

	ServiceType    string `json:"service_type"`
	ServiceVersion string `json:"service_version"`

	Status       string `json:"status"`
	RegisteredAt int64  `json:"registered_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

const (
	serviceDiscoveryLock = "service_discovery_lock"
	serviceDiscoveryMap  = "service_discovery_map"
)

func newServiceDiscovery(redisClient *redis.Client) *serviceDiscovery {
	rs := redsync.New(goredis.NewPool(redisClient))

	return &serviceDiscovery{
		redis:     redisClient,
		mx:        rs.NewMutex(serviceDiscoveryLock),
		serviceId: uuid.NewString(),
	}
}

func (sd *serviceDiscovery) registerMyself(ctx context.Context) error {
	ok := sd.mx.Lock()
	if ok != nil {
		return nil
	}

	defer sd.mx.Unlock()

	data, err := sd.redis.Get(ctx, serviceDiscoveryMap).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}

	println(" service discovery data: ", string(data))

	var services map[string]serviceDiscoveryItem
	if len(data) > 0 {
		err = json.Unmarshal(data, &services)
		if err != nil {
			return err
		}
	} else {
		services = make(map[string]serviceDiscoveryItem)
	}

	services[sd.serviceId] = serviceDiscoveryItem{
		Version:        "v2",
		ServiceType:    "edge-server",
		ServiceVersion: "1.0.0",
		Status:         statusHealthy,
		RegisteredAt:   time.Now().Unix(),
		ExpiresAt:      time.Now().Add(60 * time.Second).Unix(),
	}

	// remove expired services
	//for id, item := range services {
	//	if item.ExpiresAt < time.Now().Unix() {
	//		// todo
	//		//delete(services, id)
	//	}
	//}

	newData, err := json.Marshal(services)
	if err != nil {
		return err
	}

	println("new service discovery data: ", string(newData))

	err = sd.redis.Set(ctx, serviceDiscoveryMap, newData, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

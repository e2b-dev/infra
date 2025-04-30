package service_discovery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"time"
)

type ServiceDiscovery struct {
	redisClient *redis.Client
	redisMx     *redsync.Mutex

	serviceId      string
	serviceType    string
	serviceStatus  string
	serviceVersion string

	nodeIp   string
	nodePort int

	logger *zap.Logger
}

const (
	schemaVersion            = "v1"
	defaultMapExpiration     = 120 * time.Second
	defaultServiceExpiration = 60 * time.Second

	// todo: just for testing
	// defaultSelfRegisterInterval   = 30 * time.Second
	defaultSelfRegisterInterval = 5 * time.Second

	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
	StatusDraining  = "draining"
)

type ServiceDiscoveryItem struct {
	SchemaVersion string `json:"schema_version"`

	ServiceType    string `json:"service_type"`
	ServiceVersion string `json:"service_version"`

	NodeIp   string `json:"node_ip"`
	NodePort int    `json:"node_port"`

	Status       string `json:"status"`
	RegisteredAt int64  `json:"registered_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

type ServiceDiscoveryConfig struct {
	RedisClient *redis.Client
	Logger      *zap.Logger

	NodeIp   string
	NodePort int

	ServiceId      string
	ServiceType    string
	ServiceVersion string
	ServiceStatus  string
}

var (
	lockError          = errors.New("failed to acquire service discovery lock")
	ServiceNotFoundErr = errors.New("service not found")
)

const (
	serviceDiscoveryLock = "service_discovery_lock"
	serviceDiscoveryMap  = "service_discovery_map"
)

func NewServiceDiscovery(conf *ServiceDiscoveryConfig) *ServiceDiscovery {
	redisLock := redsync.New(goredis.NewPool(conf.RedisClient))
	redisLockMutex := redisLock.NewMutex(serviceDiscoveryLock)

	return &ServiceDiscovery{
		logger: conf.Logger,

		redisClient: conf.RedisClient,
		redisMx:     redisLockMutex,

		nodeIp:   conf.NodeIp,
		nodePort: conf.NodePort,

		serviceId:      conf.ServiceId,
		serviceStatus:  conf.ServiceStatus, // just initial status
		serviceVersion: conf.ServiceVersion,
		serviceType:    conf.ServiceType,
	}
}

func (sd *ServiceDiscovery) ListNodes(ctx context.Context) (map[string]ServiceDiscoveryItem, error) {
	err := sd.redisMx.Lock()
	if err != nil {
		return nil, lockError
	}

	defer sd.redisMx.Unlock()

	data, err := sd.redisClient.Get(ctx, serviceDiscoveryMap).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	var services map[string]ServiceDiscoveryItem
	if len(data) > 0 {
		err = json.Unmarshal(data, &services)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, nil
	}

	nodes := make(map[string]ServiceDiscoveryItem)
	for serviceId, serviceData := range services {
		nodes[serviceId] = serviceData
	}

	return nodes, nil
}

func (sd *ServiceDiscovery) GetItself() string {
	return sd.serviceId
}

func (sd *ServiceDiscovery) GetNodeById(ctx context.Context, nodeId string) (*ServiceDiscoveryItem, error) {
	err := sd.redisMx.Lock()
	if err != nil {
		return nil, lockError
	}

	defer sd.redisMx.Unlock()

	data, err := sd.redisClient.Get(ctx, serviceDiscoveryMap).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	var services map[string]ServiceDiscoveryItem
	if len(data) > 0 {
		err = json.Unmarshal(data, &services)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, nil
	}

	serviceItem, serviceFound := services[nodeId]
	if !serviceFound {
		return nil, ServiceNotFoundErr
	}

	return &serviceItem, nil
}

func (sd *ServiceDiscovery) StartSelfRegistration(ctx context.Context) {
	ticker := time.NewTicker(defaultSelfRegisterInterval)
	defer ticker.Stop()

	sd.logger.Info("starting self registration")

	for {
		select {
		case <-ctx.Done():
			sd.logger.Info("stopping self registration")
			return
		case <-ticker.C:
			err := sd.selfRegistration(ctx)
			if err != nil {
				sd.logger.Error("failed to self-register service", zap.Error(err))
			} else {
				sd.logger.Debug("self-registration successful")
			}
		}
	}
}

func (sd *ServiceDiscovery) SetStatus(status string) {
	sd.serviceStatus = status
}

func (sd *ServiceDiscovery) selfRegistration(ctx context.Context) error {
	err := sd.redisMx.Lock()
	if err != nil {
		return lockError
	}

	defer sd.redisMx.Unlock()

	sd.logger.Info("starting self registration")

	data, err := sd.redisClient.Get(ctx, serviceDiscoveryMap).Bytes()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}

	var services map[string]ServiceDiscoveryItem
	if len(data) > 0 {
		err = json.Unmarshal(data, &services)
		if err != nil {
			return err
		}
	} else {
		services = make(map[string]ServiceDiscoveryItem)
	}

	serviceRegistration := time.Now()
	serviceExpiration := time.Now().Add(defaultServiceExpiration)

	// just copy the registration time from the existing service if present
	serviceItem, serviceFound := services[sd.serviceId]
	if serviceFound {
		sd.logger.Debug("service was found, just copping registration type")
		serviceRegistration = time.Unix(serviceItem.RegisteredAt, 0)
	}

	services[sd.serviceId] = ServiceDiscoveryItem{
		SchemaVersion: schemaVersion,

		ServiceType:    sd.serviceType,
		ServiceVersion: sd.serviceVersion,
		Status:         sd.serviceStatus,

		NodeIp:   sd.nodeIp,
		NodePort: sd.nodePort,

		RegisteredAt: serviceRegistration.Unix(),
		ExpiresAt:    serviceExpiration.Unix(),
	}

	// remove already expired services
	for id, service := range services {
		if time.Now().Unix() > service.ExpiresAt {
			sd.logger.Debug("removing expired service", zap.String("service_id", id))
			delete(services, id)
		}
	}

	adjustedData, err := json.Marshal(services)
	if err != nil {
		return err
	}

	err = sd.redisClient.Set(ctx, serviceDiscoveryMap, adjustedData, defaultMapExpiration).Err()
	if err != nil {
		return err
	}

	return nil
}

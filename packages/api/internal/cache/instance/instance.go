package instance

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/analytics"
	"github.com/e2b-dev/infra/packages/api/internal/api"
)

const (
	InstanceExpiration = time.Second * 15
	CacheSyncTime      = time.Minute
)

type InstanceInfo struct {
	Instance          *api.Sandbox
	TeamID            *uuid.UUID
	BuildID           *uuid.UUID
	Metadata          map[string]string
	MaxInstanceLength time.Duration
	StartTime         time.Time
	EndTime           time.Time
	VCpu              int64
	RamMB             int64
}

type InstanceCache struct {
	reservations *ReservationCache

	cache *ttlcache.Cache[string, InstanceInfo]

	logger *zap.SugaredLogger

	counter   metric.Int64UpDownCounter
	analytics analytics.AnalyticsCollectorClient

	mu sync.Mutex
}

func NewCache(
	analytics analytics.AnalyticsCollectorClient,
	logger *zap.SugaredLogger,
	insertInstance func(data InstanceInfo) error,
	deleteInstance func(data InstanceInfo) error,
	counter metric.Int64UpDownCounter,
) *InstanceCache {
	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	cache := ttlcache.New(
		ttlcache.WithTTL[string, InstanceInfo](InstanceExpiration),
	)

	instanceCache := &InstanceCache{
		cache:     cache,
		counter:   counter,
		logger:    logger,
		analytics: analytics,

		reservations: NewReservationCache(),
	}

	cache.OnInsertion(func(ctx context.Context, i *ttlcache.Item[string, InstanceInfo]) {
		instanceInfo := i.Value()
		err := insertInstance(instanceInfo)
		if err != nil {
			logger.Errorf("Error inserting instance: %v", err)
		}

		instanceCache.UpdateCounter(instanceInfo, 1)
	})

	cache.OnEviction(func(ctx context.Context, er ttlcache.EvictionReason, i *ttlcache.Item[string, InstanceInfo]) {
		if er == ttlcache.EvictionReasonExpired || er == ttlcache.EvictionReasonDeleted {
			instanceInfo := i.Value()
			err := deleteInstance(instanceInfo)
			if err != nil {
				logger.Errorf("Error deleting instance (%v)\n: %v", er, err)
			}

			instanceCache.UpdateCounter(instanceInfo, -1)
		}
	})

	go cache.Start()

	return instanceCache
}

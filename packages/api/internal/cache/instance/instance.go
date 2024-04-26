package instance

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
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
	StartTime         *time.Time
	MaxInstanceLength time.Duration
	VCPU              int64
	RamMB             int64
}

type InstanceCache struct {
	reservations *ReservationCache

	cache *ttlcache.Cache[string, InstanceInfo]

	logger *zap.SugaredLogger

	counter   metric.Int64UpDownCounter
	analytics analyticscollector.AnalyticsCollectorClient

	mu sync.Mutex
}

func NewCache(
	analytics analyticscollector.AnalyticsCollectorClient,
	logger *zap.SugaredLogger,
	insertInstance func(data InstanceInfo) error,
	deleteInstance func(data InstanceInfo) error,
	initialInstances []*InstanceInfo,
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
		err := insertInstance(i.Value())
		if err != nil {
			logger.Errorf("Error inserting instance: %v", err.Err)
		}
	})
	cache.OnEviction(func(ctx context.Context, er ttlcache.EvictionReason, i *ttlcache.Item[string, InstanceInfo]) {
		if er == ttlcache.EvictionReasonExpired || er == ttlcache.EvictionReasonDeleted {
			err := deleteInstance(i.Value())
			if err != nil {
				logger.Errorf("Error deleting instance (%v)\n: %v", er, err.Err)
			}

			instanceCache.UpdateCounter(i.Value(), -1)
		}
	})

	for _, instance := range initialInstances {
		err := instanceCache.Add(*instance)
		if err != nil {
			fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
		}
	}

	go cache.Start()

	return instanceCache
}

package instance

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	InstanceExpiration      = time.Second * 15
	CacheSyncTime           = time.Minute * 3
	instanceCountMeterName  = "api.env.instance.running"
	instanceCreateMeterName = "api.env.instance.started"
)

type InstanceInfo struct {
	Logger            *logs.SandboxLogger
	Instance          *api.Sandbox
	TeamID            *uuid.UUID
	BuildID           *uuid.UUID
	Metadata          map[string]string
	MaxInstanceLength time.Duration
	StartTime         time.Time
	EndTime           time.Time
}

type InstanceCache struct {
	reservations *ReservationCache

	cache *ttlcache.Cache[string, InstanceInfo]

	logger *zap.SugaredLogger

	analytics analyticscollector.AnalyticsCollectorClient

	mu sync.Mutex
}

func NewCache(analytics analyticscollector.AnalyticsCollectorClient, logger *zap.SugaredLogger, deleteInstance func(data InstanceInfo, purge bool) *api.APIError, initialInstances []*InstanceInfo) *InstanceCache {
	err := meters.CreateCounter(
		"api.env.instance.started",
		"Counter of started instances.",
		"{instance}")
	if err != nil {
		panic(fmt.Errorf("error creating counter: %w", err))
	}

	err = meters.CreateUpDownCounter(
		"api.env.instance.running",
		"Number of running instances.",
		"{instance}")
	if err != nil {
		panic(fmt.Errorf("error creating counter: %w", err))
	}

	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	cache := ttlcache.New(
		ttlcache.WithTTL[string, InstanceInfo](InstanceExpiration),
	)

	instanceCache := &InstanceCache{
		cache:     cache,
		logger:    logger,
		analytics: analytics,

		reservations: NewReservationCache(),
	}

	cache.OnInsertion(func(ctx context.Context, i *ttlcache.Item[string, InstanceInfo]) {
		instanceInfo := i.Value()
		_, err := analytics.InstanceStarted(ctx, &analyticscollector.InstanceStartedEvent{
			InstanceId:    instanceInfo.Instance.SandboxID,
			EnvironmentId: instanceInfo.Instance.TemplateID,
			BuildId:       instanceInfo.BuildID.String(),
			TeamId:        instanceInfo.TeamID.String(),
			Timestamp:     timestamppb.Now(),
		})
		if err != nil {
			errMsg := fmt.Errorf("error when sending analytics event: %w", err)
			telemetry.ReportCriticalError(ctx, errMsg)
		}
	})

	cache.OnEviction(func(ctx context.Context, er ttlcache.EvictionReason, i *ttlcache.Item[string, InstanceInfo]) {
		if er == ttlcache.EvictionReasonExpired || er == ttlcache.EvictionReasonDeleted {
			err := deleteInstance(i.Value(), true)
			if err != nil {
				logger.Errorf("Error deleting instance (%v)\n: %v", er, err.Err)
			}

			instanceCache.UpdateCounter(i.Value(), -1, false)
		}
	})

	for _, instance := range initialInstances {
		err := instanceCache.Add(*instance, true)
		if err != nil {
			fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
		}
	}

	go cache.Start()

	return instanceCache
}

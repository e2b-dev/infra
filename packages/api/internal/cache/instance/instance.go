package instance

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/api/internal/meters"
	"go.opentelemetry.io/otel/metric"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	InstanceExpiration = time.Second * 15
	CacheSyncTime      = time.Minute * 3
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

	sandboxCounter metric.Int64UpDownCounter
	createdCounter metric.Int64Counter
	analytics      analyticscollector.AnalyticsCollectorClient

	mu sync.Mutex
}

func NewCache(analytics analyticscollector.AnalyticsCollectorClient, logger *zap.SugaredLogger, deleteInstance func(data InstanceInfo, purge bool) *api.APIError, initialInstances []*InstanceInfo) *InstanceCache {
	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	cache := ttlcache.New(
		ttlcache.WithTTL[string, InstanceInfo](InstanceExpiration),
	)

	sandboxCounter, err := meters.GetUpDownCounter(meters.SandboxCountMeterName)
	if err != nil {
		logger.Errorw("error getting counter", "error", err)
	}

	createdCounter, err := meters.GetCounter(meters.SandboxCreateMeterName)
	if err != nil {
		logger.Errorw("error getting counter", "error", err)
	}

	instanceCache := &InstanceCache{
		cache:          cache,
		logger:         logger,
		analytics:      analytics,
		sandboxCounter: sandboxCounter,
		createdCounter: createdCounter,
		reservations:   NewReservationCache(),
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

			instanceCache.UpdateCounters(i.Value(), -1, false)
		}
	})

	for _, instance := range initialInstances {
		err := instanceCache.Add(*instance, false)
		if err != nil {
			fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
		}
	}

	go cache.Start()

	return instanceCache
}

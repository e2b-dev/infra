package instance

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	InstanceExpiration = time.Second * 15
	CacheSyncTime      = time.Minute * 3
)

type InstanceInfo struct {
	Instance          *api.Sandbox
	TeamID            *uuid.UUID
	BuildID           *uuid.UUID
	Metadata          map[string]string
	StartTime         *time.Time
	MaxInstanceLength time.Duration
}

type InstanceCache struct {
	reservations *ReservationCache

	cache *ttlcache.Cache[string, InstanceInfo]

	logger *zap.SugaredLogger

	analytics analyticscollector.AnalyticsCollectorClient

	mu sync.Mutex
}

// We will need to either use Redis for storing active instances OR retrieve them from Nomad when we start API to keep everything in sync
// We are retrieving the tasks from Nomad now.
func NewCache(
	analytics analyticscollector.AnalyticsCollectorClient,
	logger *zap.SugaredLogger,
	deleteInstance func(data InstanceInfo, purge bool) *api.APIError,
	initialInstances []*InstanceInfo,
	counter metric.Int64ObservableUpDownCounter,
	meter metric.Meter,
) (*InstanceCache, error) {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, InstanceInfo](InstanceExpiration),
	)

	instanceCache := &InstanceCache{
		cache:     cache,
		logger:    logger,
		analytics: analytics,

		reservations: NewReservationCache(),
	}

	_, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		if instanceCache.cache.Len() == 0 {
			o.ObserveInt64(counter, 0)
		} else {
			for _, item := range instanceCache.cache.Items() {
				instance := item.Value()
				o.ObserveInt64(
					counter,
					1,
					metric.WithAttributes(
						attribute.String("instance_id", instance.Instance.SandboxID),
						attribute.String("env_id", instance.Instance.TemplateID),
						attribute.String("team_id", instance.TeamID.String()),
					))
			}
		}
		return nil
	}, counter)
	if err != nil {
		return nil, fmt.Errorf("error registering callback: %w", err)
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
		}
	})

	for _, instance := range initialInstances {
		err := instanceCache.Add(*instance)
		if err != nil {
			fmt.Println(fmt.Errorf("error adding instance to cache: %w", err))
		}
	}

	go cache.Start()

	return instanceCache, nil
}

package instance

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
)

const (
	InstanceExpiration = time.Second * 15
	InstanceAutoPause  = false
	CacheSyncTime      = time.Minute
)

type InstanceInfo struct {
	Logger             *logs.SandboxLogger
	Instance           *api.Sandbox
	TeamID             *uuid.UUID
	BuildID            *uuid.UUID
	Metadata           map[string]string
	MaxInstanceLength  time.Duration
	StartTime          time.Time
	EndTime            time.Time
	VCpu               int64
	TotalDiskSizeMB    int64
	RamMB              int64
	KernelVersion      string
	FirecrackerVersion string
	EnvdVersion        string
	Node               *node.NodeInfo
	AutoPause          bool
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

func NewCache(
	analytics analyticscollector.AnalyticsCollectorClient,
	logger *zap.SugaredLogger,
	insertInstance func(data InstanceInfo) error,
	deleteInstance func(data InstanceInfo) error,
) *InstanceCache {
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
		err := insertInstance(instanceInfo)
		if err != nil {
			logger.Errorf("Error inserting instance: %v", err)

			return
		}
	})

	cache.OnEviction(func(ctx context.Context, er ttlcache.EvictionReason, i *ttlcache.Item[string, InstanceInfo]) {
		if er == ttlcache.EvictionReasonExpired || er == ttlcache.EvictionReasonDeleted {
			instanceInfo := i.Value()
			err := deleteInstance(instanceInfo)
			if err != nil {
				logger.Errorf("Error deleting instance (%v)\n: %v", er, err)
			}

			instanceCache.UpdateCounters(i.Value(), -1, false)
		}
	})

	go cache.Start()

	return instanceCache
}

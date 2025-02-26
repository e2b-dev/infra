package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	InstanceExpiration = time.Second * 15
	// Should we auto pause the instance by default instead of killing it,
	InstanceAutoPauseDefault = false
	CacheSyncTime            = time.Minute
)

var (
	ErrPausingInstanceNotFound = errors.New("pausing instance not found")
)

type InstanceInfo struct {
	Logger             *sbxlogger.SandboxLogger
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
	AutoPause          *bool
	Pausing            *utils.SetOnce[*node.NodeInfo]
}

type InstanceCache struct {
	reservations *ReservationCache
	pausing      *smap.Map[*InstanceInfo]

	cache *ttlcache.Cache[string, InstanceInfo]

	sandboxCounter metric.Int64UpDownCounter
	createdCounter metric.Int64Counter
	analytics      analyticscollector.AnalyticsCollectorClient

	mu sync.Mutex
}

func NewCache(
	analytics analyticscollector.AnalyticsCollectorClient,
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
		zap.L().Error("error getting counter", zap.Error(err))
	}

	createdCounter, err := meters.GetCounter(meters.SandboxCreateMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))
	}

	instanceCache := &InstanceCache{
		cache:          cache,
		analytics:      analytics,
		sandboxCounter: sandboxCounter,
		createdCounter: createdCounter,
		reservations:   NewReservationCache(),
		pausing:        smap.New[*InstanceInfo](),
	}

	cache.OnInsertion(func(ctx context.Context, i *ttlcache.Item[string, InstanceInfo]) {
		instanceInfo := i.Value()
		err := insertInstance(instanceInfo)
		if err != nil {
			zap.L().Error("Error inserting instance", zap.Error(err))

			return
		}
	})

	cache.OnEviction(func(ctx context.Context, er ttlcache.EvictionReason, i *ttlcache.Item[string, InstanceInfo]) {
		if er == ttlcache.EvictionReasonExpired || er == ttlcache.EvictionReasonDeleted {
			instanceInfo := i.Value()
			err := deleteInstance(instanceInfo)
			if err != nil {
				zap.L().Error("Error deleting instance", zap.Error(err))
			}

			instanceCache.UpdateCounters(i.Value(), -1, false)
		}
	})

	go cache.Start()

	return instanceCache
}

func (c *InstanceCache) MarkAsPausing(instanceInfo *InstanceInfo) {
	if instanceInfo.AutoPause == nil {
		return
	}

	if *instanceInfo.AutoPause {
		c.pausing.InsertIfAbsent(instanceInfo.Instance.SandboxID, instanceInfo)
	}
}

func (c *InstanceCache) UnmarkAsPausing(instanceInfo *InstanceInfo) {
	c.pausing.RemoveCb(instanceInfo.Instance.SandboxID, func(key string, v *InstanceInfo, exists bool) bool {
		if !exists {
			return false
		}

		// We depend of the startTime not changing to uniquely identify instance in the cache.
		return v.Instance.SandboxID == instanceInfo.Instance.SandboxID && v.StartTime == instanceInfo.StartTime
	})
}

func (c *InstanceCache) WaitForPause(ctx context.Context, sandboxID string) (*node.NodeInfo, error) {
	instanceInfo, ok := c.pausing.Get(sandboxID)
	if !ok {
		return nil, ErrPausingInstanceNotFound
	}

	value, err := instanceInfo.Pausing.WaitWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("pause waiting was canceled: %w", err)
	}

	return value, nil
}

func (c *InstanceInfo) PauseDone(err error) {
	if err == nil {
		err := c.Pausing.SetValue(c.Node)
		if err != nil {
			zap.L().Error("error setting PauseDone value", zap.Error(err))

			return
		}
	} else {
		err := c.Pausing.SetError(err)
		if err != nil {
			zap.L().Error("error setting PauseDone error", zap.Error(err))

			return
		}
	}
}

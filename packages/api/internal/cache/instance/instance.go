package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
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
)

var (
	ErrPausingInstanceNotFound = errors.New("pausing instance not found")
)

func NewInstanceInfo(
	Instance *api.Sandbox,
	ExecutionID string,
	TeamID *uuid.UUID,
	BuildID *uuid.UUID,
	Metadata map[string]string,
	MaxInstanceLength time.Duration,
	StartTime time.Time,
	endTime time.Time,
	VCpu int64,
	TotalDiskSizeMB int64,
	RamMB int64,
	KernelVersion string,
	FirecrackerVersion string,
	EnvdVersion string,
	Node *node.NodeInfo,
	AutoPause bool,
	EnvdAccessToken *string,
	BaseTemplateID string,
) *InstanceInfo {
	instance := &InstanceInfo{
		Instance:           Instance,
		ExecutionID:        ExecutionID,
		TeamID:             TeamID,
		BuildID:            BuildID,
		Metadata:           Metadata,
		MaxInstanceLength:  MaxInstanceLength,
		StartTime:          StartTime,
		endTime:            endTime,
		VCpu:               VCpu,
		TotalDiskSizeMB:    TotalDiskSizeMB,
		RamMB:              RamMB,
		KernelVersion:      KernelVersion,
		FirecrackerVersion: FirecrackerVersion,
		EnvdVersion:        EnvdVersion,
		EnvdAccessToken:    EnvdAccessToken,
		Node:               Node,
		AutoPause:          atomic.Bool{},
		Pausing:            utils.NewSetOnce[*node.NodeInfo](),
		BaseTemplateID:     BaseTemplateID,
		mu:                 sync.RWMutex{},
	}

	instance.AutoPause.Store(AutoPause)

	return instance
}

type InstanceInfo struct {
	Instance           *api.Sandbox
	ExecutionID        string
	TeamID             *uuid.UUID
	BuildID            *uuid.UUID
	BaseTemplateID     string
	Metadata           map[string]string
	MaxInstanceLength  time.Duration
	StartTime          time.Time
	endTime            time.Time
	VCpu               int64
	TotalDiskSizeMB    int64
	RamMB              int64
	KernelVersion      string
	FirecrackerVersion string
	EnvdVersion        string
	EnvdAccessToken    *string
	Node               *node.NodeInfo
	AutoPause          atomic.Bool
	Pausing            *utils.SetOnce[*node.NodeInfo]
	mu                 sync.RWMutex
}

func (i *InstanceInfo) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  i.Instance.SandboxID,
		TemplateID: i.Instance.TemplateID,
		TeamID:     i.TeamID.String(),
	}
}

func (i *InstanceInfo) IsExpired() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return time.Now().After(i.endTime)
}

func (i *InstanceInfo) GetEndTime() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i.endTime
}

func (i *InstanceInfo) SetEndTime(endTime time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.endTime = endTime
}

func (i *InstanceInfo) SetExpired() {
	i.SetEndTime(time.Now())
}

type InstanceCache struct {
	reservations *ReservationCache
	pausing      *smap.Map[*InstanceInfo]

	cache          *lifecycleCache[*InstanceInfo]
	insertInstance func(data *InstanceInfo) error

	sandboxCounter metric.Int64UpDownCounter
	createdCounter metric.Int64Counter
	analytics      analyticscollector.AnalyticsCollectorClient

	mu sync.Mutex
}

func NewCache(
	ctx context.Context,
	analytics analyticscollector.AnalyticsCollectorClient,
	insertInstance func(data *InstanceInfo) error,
	deleteInstance func(data *InstanceInfo) error,
) *InstanceCache {
	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	cache := newLifecycleCache[*InstanceInfo]()

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
		insertInstance: insertInstance,
		analytics:      analytics,
		sandboxCounter: sandboxCounter,
		createdCounter: createdCounter,
		reservations:   NewReservationCache(),
		pausing:        smap.New[*InstanceInfo](),
	}

	cache.OnEviction(func(ctx context.Context, instanceInfo *InstanceInfo) {
		err := deleteInstance(instanceInfo)
		if err != nil {
			zap.L().Error("Error deleting instance", zap.Error(err))
		}

		instanceCache.UpdateCounters(ctx, instanceInfo, -1, false)
	})

	go cache.Start(ctx)

	return instanceCache
}

func (c *InstanceCache) Len() int {
	return c.cache.Len()
}

func (c *InstanceCache) Set(key string, value *InstanceInfo) {
	inserted := c.cache.SetIfAbsent(key, value)
	if inserted {
		go func() {
			err := c.insertInstance(value)
			if err != nil {
				zap.L().Error("error inserting instance", zap.Error(err))
			}
		}()
	}
}

func (c *InstanceCache) MarkAsPausing(instanceInfo *InstanceInfo) {
	if instanceInfo.AutoPause.Load() {
		c.pausing.InsertIfAbsent(instanceInfo.Instance.SandboxID, instanceInfo)
	}
}

func (c *InstanceCache) UnmarkAsPausing(instanceInfo *InstanceInfo) {
	c.pausing.RemoveCb(instanceInfo.Instance.SandboxID, func(key string, v *InstanceInfo, exists bool) bool {
		if !exists {
			return false
		}

		// Make sure it's the same instance and not a sandbox which has been already resumed
		return v.ExecutionID == instanceInfo.ExecutionID
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

func (i *InstanceInfo) PauseDone(err error) {
	if err == nil {
		err := i.Pausing.SetValue(i.Node)
		if err != nil {
			zap.L().Error("error setting PauseDone value", zap.Error(err))

			return
		}
	} else {
		err := i.Pausing.SetError(err)
		if err != nil {
			zap.L().Error("error setting PauseDone error", zap.Error(err))

			return
		}
	}
}

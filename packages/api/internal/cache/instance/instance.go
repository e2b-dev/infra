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

	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	InstanceExpiration = time.Second * 15
	// Should we auto pause the instance by default instead of killing it,
	InstanceAutoPauseDefault = false
)

var ErrPausingInstanceNotFound = errors.New("pausing instance not found")

func NewInstanceInfo(
	SandboxID string,
	TemplateID string,
	ClientID string,
	Alias *string,
	ExecutionID string,
	TeamID uuid.UUID,
	BuildID uuid.UUID,
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
	NodeID string,
	ClusterID uuid.UUID,
	AutoPause bool,
	EnvdAccessToken *string,
	allowInternetAccess *bool,
	BaseTemplateID string,
) *InstanceInfo {
	instance := &InstanceInfo{
		SandboxID:  SandboxID,
		TemplateID: TemplateID,
		ClientID:   ClientID,
		Alias:      Alias,

		ExecutionID:         ExecutionID,
		TeamID:              TeamID,
		BuildID:             BuildID,
		Metadata:            Metadata,
		MaxInstanceLength:   MaxInstanceLength,
		StartTime:           StartTime,
		endTime:             endTime,
		VCpu:                VCpu,
		TotalDiskSizeMB:     TotalDiskSizeMB,
		RamMB:               RamMB,
		KernelVersion:       KernelVersion,
		FirecrackerVersion:  FirecrackerVersion,
		EnvdVersion:         EnvdVersion,
		EnvdAccessToken:     EnvdAccessToken,
		AllowInternetAccess: allowInternetAccess,
		NodeID:              NodeID,
		ClusterID:           ClusterID,
		AutoPause:           atomic.Bool{},
		Pausing:             utils.NewSetOnce[string](),
		BaseTemplateID:      BaseTemplateID,
	}

	instance.AutoPause.Store(AutoPause)

	return instance
}

type InstanceInfo struct {
	SandboxID  string
	TemplateID string
	ClientID   string
	Alias      *string

	ExecutionID         string
	TeamID              uuid.UUID
	BuildID             uuid.UUID
	BaseTemplateID      string
	metadata            map[string]string
	MaxInstanceLength   time.Duration
	StartTime           time.Time
	endTime             time.Time
	VCpu                int64
	TotalDiskSizeMB     int64
	RamMB               int64
	KernelVersion       string
	FirecrackerVersion  string
	EnvdVersion         string
	EnvdAccessToken     *string
	AllowInternetAccess *bool
	NodeID              string
	ClusterID           uuid.UUID
	AutoPause           atomic.Bool
	Pausing             *utils.SetOnce[string]
	sync.RWMutex
}

func (i *InstanceInfo) LoggerMetadata() sbxlogger.SandboxMetadata {
	return sbxlogger.SandboxMetadata{
		SandboxID:  i.SandboxID,
		TemplateID: i.TemplateID,
		TeamID:     i.TeamID.String(),
	}
}

func (i *InstanceInfo) IsExpired() bool {
	i.RLock()
	defer i.RUnlock()

	return time.Now().After(i.endTime)
}

func (i *InstanceInfo) Metadata() map[string]string {
	i.RLock()
	defer i.RUnlock()

	return i.metadata
}

func (i *InstanceInfo) UpdateMetadata(metadata map[string]string) {
	i.Lock()
	defer i.Unlock()

	i.metadata = metadata
}

func (i *InstanceInfo) GetEndTime() time.Time {
	i.RLock()
	defer i.RUnlock()

	return i.endTime
}

func (i *InstanceInfo) SetEndTime(endTime time.Time) {
	i.Lock()
	defer i.Unlock()

	i.endTime = endTime
}

func (i *InstanceInfo) SetExpired() {
	i.SetEndTime(time.Now())
}

type InstanceCache struct {
	reservations *ReservationCache
	pausing      *smap.Map[*InstanceInfo]

	cache          *lifecycleCache[*InstanceInfo]
	insertInstance func(data *InstanceInfo, created bool) error

	sandboxCounter metric.Int64UpDownCounter
	createdCounter metric.Int64Counter

	mu sync.Mutex
}

func NewCache(
	ctx context.Context,
	meterProvider metric.MeterProvider,
	insertInstance func(data *InstanceInfo, created bool) error,
	deleteInstance func(data *InstanceInfo) error,
) *InstanceCache {
	// We will need to either use Redis or Consul's KV for storing active sandboxes to keep everything in sync,
	// right now we load them from Orchestrator
	cache := newLifecycleCache[*InstanceInfo]()

	meter := meterProvider.Meter("api.cache.sandbox")
	sandboxCounter, err := telemetry.GetUpDownCounter(meter, telemetry.SandboxCountMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))
	}

	createdCounter, err := telemetry.GetCounter(meter, telemetry.SandboxCreateMeterName)
	if err != nil {
		zap.L().Error("error getting counter", zap.Error(err))
	}

	instanceCache := &InstanceCache{
		cache:          cache,
		insertInstance: insertInstance,
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

func (c *InstanceCache) Set(key string, value *InstanceInfo, created bool) {
	inserted := c.cache.SetIfAbsent(key, value)
	if inserted {
		go func() {
			err := c.insertInstance(value, created)
			if err != nil {
				zap.L().Error("error inserting instance", zap.Error(err))
			}
		}()
	}
}

func (c *InstanceCache) MarkAsPausing(instanceInfo *InstanceInfo) {
	if instanceInfo.AutoPause.Load() {
		c.pausing.InsertIfAbsent(instanceInfo.SandboxID, instanceInfo)
	}
}

func (c *InstanceCache) UnmarkAsPausing(instanceInfo *InstanceInfo) {
	c.pausing.RemoveCb(instanceInfo.SandboxID, func(key string, v *InstanceInfo, exists bool) bool {
		if !exists {
			return false
		}

		// Make sure it's the same instance and not a sandbox which has been already resumed
		return v.ExecutionID == instanceInfo.ExecutionID
	})
}

// WaitForPause waits for the instance to be paused. Returns the node ID of the node that paused the instance.
func (c *InstanceCache) WaitForPause(ctx context.Context, sandboxID string) (nodeID string, err error) {
	instanceInfo, ok := c.pausing.Get(sandboxID)
	if !ok {
		return "", ErrPausingInstanceNotFound
	}

	nodeID, err = instanceInfo.Pausing.WaitWithContext(ctx)
	if err != nil {
		return "", fmt.Errorf("pause waiting was canceled: %w", err)
	}

	return
}

func (i *InstanceInfo) PauseDone(err error) {
	if err == nil {
		err := i.Pausing.SetValue(i.NodeID)
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

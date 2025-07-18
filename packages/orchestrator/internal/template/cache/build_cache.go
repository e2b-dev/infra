package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	buildInfoExpiration = time.Minute * 10 // 10 minutes
)

var ErrCancelledBuild = errors.New("build was cancelled")

type BuildInfo struct {
	status   template_manager.TemplateBuildState
	reason   *string
	metadata *template_manager.TemplateBuildMetadata
	logs     *SafeBuffer
	mu       sync.RWMutex
	Cancel   *utils.SetOnce[struct{}]
}

func (b *BuildInfo) isRunningWithoutLock() bool {
	return b.status == template_manager.TemplateBuildState_Building
}

func (b *BuildInfo) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.isRunningWithoutLock()
}

func (b *BuildInfo) IsFailed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.status == template_manager.TemplateBuildState_Failed
}

func (b *BuildInfo) GetMetadata() *template_manager.TemplateBuildMetadata {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.metadata
}

func (b *BuildInfo) GetStatus() template_manager.TemplateBuildState {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.status
}

func (b *BuildInfo) GetReason() *string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.reason
}

func (b *BuildInfo) SetSuccess(metadata *template_manager.TemplateBuildMetadata) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isRunningWithoutLock() {
		return
	}

	b.status = template_manager.TemplateBuildState_Completed
	b.metadata = metadata
	b.reason = nil
}

func (b *BuildInfo) SetFail(reason *string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isRunningWithoutLock() {
		return
	}

	b.status = template_manager.TemplateBuildState_Failed
	b.reason = reason
}

func (b *BuildInfo) GetLogs() []*template_manager.TemplateBuildLogEntry {
	return b.logs.Lines()
}

type BuildCache struct {
	cache *ttlcache.Cache[string, *BuildInfo]
}

func NewBuildCache(meterProvider metric.MeterProvider) *BuildCache {
	meter := meterProvider.Meter("orchestrator.cache.build")

	cache := ttlcache.New(ttlcache.WithTTL[string, *BuildInfo](buildInfoExpiration))
	_, err := telemetry.GetObservableUpDownCounter(meter, telemetry.BuildCounterMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		items := utils.MapValues(cache.Items())

		// Filter running builds
		runningCount := len(utils.Filter(items, func(item *ttlcache.Item[string, *BuildInfo]) bool {
			return item != nil && item.Value() != nil && item.Value().IsRunning()
		}))

		observer.Observe(int64(runningCount))
		return nil
	})
	if err != nil {
		zap.L().Error("error creating counter", zap.Error(err), zap.Any("counter_name", telemetry.BuildCounterMeterName))
	}

	go cache.Start()

	return &BuildCache{
		cache: cache,
	}
}

// Get returns the build info.
func (c *BuildCache) Get(buildID string) (*BuildInfo, error) {
	item := c.cache.Get(buildID)
	if item == nil {
		return nil, fmt.Errorf("build %s not found in cache", buildID)
	}

	value := item.Value()
	if value == nil {
		return nil, fmt.Errorf("build %s not found in cache", buildID)
	}

	return value, nil
}

// Create creates a new build if it doesn't exist in the cache or the build was already finished.
func (c *BuildCache) Create(buildID string, logs *SafeBuffer) (*BuildInfo, error) {
	info := &BuildInfo{
		status:   template_manager.TemplateBuildState_Building,
		metadata: nil,
		logs:     logs,
		Cancel:   utils.NewSetOnce[struct{}](),
	}

	_, found := c.cache.GetOrSet(buildID, info,
		ttlcache.WithTTL[string, *BuildInfo](buildInfoExpiration),
		ttlcache.WithDisableTouchOnHit[string, *BuildInfo](),
	)
	if found {
		return nil, fmt.Errorf("build %s already exists in cache", buildID)
	}

	return info, nil
}

func (c *BuildCache) Delete(buildID string) {
	c.cache.Delete(buildID)
}

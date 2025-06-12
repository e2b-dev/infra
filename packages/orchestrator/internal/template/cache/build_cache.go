package cache

import (
	"context"
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

type BuildInfo struct {
	status    template_manager.TemplateBuildState
	metadata  *template_manager.TemplateBuildMetadata
	mu        sync.RWMutex
	ctx       context.Context
	ctxCancel context.CancelFunc
}

func (b *BuildInfo) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.status == template_manager.TemplateBuildState_Building
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

func (b *BuildInfo) GetContext() context.Context {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.ctx
}

func (b *BuildInfo) Cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.ctxCancel()
}

type BuildCache struct {
	cache *ttlcache.Cache[string, *BuildInfo]

	mu sync.Mutex
}

func NewBuildCache(meterProvider metric.MeterProvider) *BuildCache {
	meter := meterProvider.Meter("build-cache")

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
func (c *BuildCache) Create(buildID string) (*BuildInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.cache.Get(buildID, ttlcache.WithDisableTouchOnHit[string, *BuildInfo]())
	if item != nil {
		return nil, fmt.Errorf("build %s already exists in cache", buildID)
	}

	ctx, cancel := context.WithCancel(context.Background())

	info := &BuildInfo{
		status:    template_manager.TemplateBuildState_Building,
		metadata:  nil,
		ctx:       ctx,
		ctxCancel: cancel,
	}

	c.cache.Set(buildID, info, buildInfoExpiration)

	return info, nil
}

func (c *BuildCache) SetSucceeded(buildID string, metadata *template_manager.TemplateBuildMetadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	item.status = template_manager.TemplateBuildState_Completed
	item.metadata = metadata
	return nil
}

func (c *BuildCache) SetFailed(buildID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	item.status = template_manager.TemplateBuildState_Failed
	return nil
}

func (c *BuildCache) Delete(buildID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache.Delete(buildID)
}

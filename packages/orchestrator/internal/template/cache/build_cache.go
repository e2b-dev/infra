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
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	buildInfoExpiration = time.Minute * 10 // 10 minutes
)

type BuildInfo struct {
	envID     string
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

func (b *BuildInfo) GetBuildEnvID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.envID
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

func NewBuildCache() *BuildCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *BuildInfo](buildInfoExpiration))
	_, err := meters.GetObservableUpDownCounter(meters.BuildCounterMeterName, func(ctx context.Context, observer metric.Int64Observer) error {
		cacheItems := utils.MapValues(cache.Items())
		observer.Observe(int64(len(
			utils.Filter(cacheItems, func(items *ttlcache.Item[string, *BuildInfo]) bool {
				if items == nil || items.Value() == nil {
					return false
				}

				// Only count builds that are still running
				return items.Value().IsRunning()
			}),
		)))

		return nil
	})
	if err != nil {
		zap.L().Error("error creating counter", zap.Error(err))
	}

	go cache.Start()

	return &BuildCache{
		cache: cache,
	}
}

// Get returns the build info.
func (c *BuildCache) Get(buildIDOrEnvID string) (*BuildInfo, error) {
	item := c.cache.Get(buildIDOrEnvID)
	if item == nil {
		return nil, fmt.Errorf("build %s not found in cache", buildIDOrEnvID)
	}

	value := item.Value()
	if value == nil {
		return nil, fmt.Errorf("build %s not found in cache", buildIDOrEnvID)
	}

	return value, nil
}

// Create creates a new build if it doesn't exist in the cache or the build was already finished.
func (c *BuildCache) Create(envID string, buildID string) (*BuildInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.cache.Get(buildID, ttlcache.WithDisableTouchOnHit[string, *BuildInfo]())
	if item != nil {
		return nil, fmt.Errorf("build %s for env %s already exists in cache", buildID, envID)
	}

	ctx, cancel := context.WithCancel(context.Background())

	info := &BuildInfo{
		envID:     envID,
		status:    template_manager.TemplateBuildState_Building,
		metadata:  nil,
		ctx:       ctx,
		ctxCancel: cancel,
	}

	c.cache.Set(buildID, info, buildInfoExpiration)
	c.cache.Set(envID, info, buildInfoExpiration)

	return info, nil
}

func (c *BuildCache) SetSucceeded(envID string, buildID string, metadata *template_manager.TemplateBuildMetadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	// Just to touch the item in the cache to update its expiration time
	_, _ = c.Get(envID)

	item.status = template_manager.TemplateBuildState_Completed
	item.metadata = metadata
	return nil
}

func (c *BuildCache) SetFailed(envID string, buildID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	// Just to touch the item in the cache to update its expiration time
	_, _ = c.Get(envID)

	item.status = template_manager.TemplateBuildState_Failed
	return nil
}

func (c *BuildCache) Delete(envID string, buildID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache.Delete(buildID)
	c.cache.Delete(envID)
}

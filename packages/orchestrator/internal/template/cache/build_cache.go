package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
)

const (
	buildInfoExpiration = time.Minute * 10 // 10 minutes
)

type BuildInfo struct {
	envID    string
	status   template_manager.TemplateBuildState
	metadata *template_manager.TemplateBuildMetadata
	mu       sync.RWMutex
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

type BuildCache struct {
	cache   *ttlcache.Cache[string, *BuildInfo]
	counter metric.Int64UpDownCounter

	mu sync.Mutex
}

func NewBuildCache() *BuildCache {
	cache := ttlcache.New(ttlcache.WithTTL[string, *BuildInfo](buildInfoExpiration))
	counter, err := meters.GetUpDownCounter(meters.BuildCounterMeterName)
	if err != nil {
		zap.L().Error("error creating counter", zap.Error(err))
	}

	go cache.Start()

	return &BuildCache{
		cache:   cache,
		counter: counter,
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
func (c *BuildCache) Create(buildID string, envID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.cache.Get(buildID, ttlcache.WithDisableTouchOnHit[string, *BuildInfo]())
	if item != nil {
		return fmt.Errorf("build %s for env %s already exists in cache", buildID, envID)
	}

	info := BuildInfo{
		envID:    envID,
		status:   template_manager.TemplateBuildState_Building,
		metadata: nil,
	}

	c.cache.Set(buildID, &info, buildInfoExpiration)
	c.updateCounter(envID, buildID, 1)

	return nil
}

func (c *BuildCache) SetSucceeded(envID string, buildID string, metadata *template_manager.TemplateBuildMetadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	item.status = template_manager.TemplateBuildState_Completed
	item.metadata = metadata
	c.updateCounter(envID, buildID, -1)
	return nil
}

func (c *BuildCache) SetFailed(envID string, buildID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	item.status = template_manager.TemplateBuildState_Failed
	c.updateCounter(envID, buildID, -1)
	return nil
}

func (c *BuildCache) updateCounter(envID string, buildID string, value int64) {
	c.counter.Add(context.Background(), value,
		metric.WithAttributes(attribute.String("env_id", envID)),
		metric.WithAttributes(attribute.String("build_id", buildID)),
	)
}

func (c *BuildCache) Delete(envID string, buildID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache.Delete(envID)
	c.updateCounter(envID, buildID, -1)
}

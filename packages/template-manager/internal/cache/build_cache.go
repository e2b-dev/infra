package cache

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"sync"
	"time"
)

const (
	buildInfoExpiration = time.Minute * 5 // 5 minutes
)

type BuildInfo struct {
	envID  string
	ended  bool
	failed bool

	rootfsSizeKey  int32
	envdVersionKey string

	mu sync.RWMutex
}

func (b *BuildInfo) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return !b.ended
}

func (b *BuildInfo) IsFailed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.failed
}

func (b *BuildInfo) SetRootFsSizeKey(size int32) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	b.rootfsSizeKey = size
}

func (b *BuildInfo) GetRootFsSizeKey() int32 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.rootfsSizeKey
}

func (b *BuildInfo) SetEnvdVersionKey(version string) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	b.envdVersionKey = version
}

func (b *BuildInfo) GetEnvdVersionKey() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.envdVersionKey
}

func (b *BuildInfo) GetBuildEnvID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.envID
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
		envID: envID,
		ended: false,
	}

	c.cache.Set(buildID, &info, buildInfoExpiration)
	c.updateCounter(envID, buildID, 1)

	return nil
}

// SetDone marks the build as ended.
func (c *BuildCache) SetDone(envID string, buildID string) error {
	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	if !item.IsRunning() {
		return fmt.Errorf("build %s is already marked as done", buildID)
	}

	item.ended = true
	item.failed = false

	c.updateCounter(envID, buildID, -1)

	return nil
}

func (c *BuildCache) SetSucceeded(envID string, buildID string) error {
	item, err := c.Get(buildID)
	if err != nil {
		return fmt.Errorf("build %s not found in cache: %w", buildID, err)
	}

	if !item.IsRunning() {
		return fmt.Errorf("build %s is already marked as done", buildID)
	}

	item.ended = true
	item.failed = true

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
	c.cache.Delete(envID)
	c.updateCounter(envID, buildID, -1)
}

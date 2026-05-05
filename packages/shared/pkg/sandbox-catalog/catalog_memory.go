package sandbox_catalog

import (
	"context"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type MemorySandboxCatalog struct {
	cache             *ttlcache.Cache[string, *SandboxInfo]
	trafficKeepalives map[string]time.Time
	mtx               sync.RWMutex
}

const (
	catalogMemoryLocalCacheTtl = time.Millisecond * 500
)

func NewMemorySandboxesCatalog() SandboxesCatalog {
	cache := ttlcache.New(ttlcache.WithTTL[string, *SandboxInfo](catalogMemoryLocalCacheTtl), ttlcache.WithDisableTouchOnHit[string, *SandboxInfo]())
	go cache.Start()

	return &MemorySandboxCatalog{
		cache:             cache,
		trafficKeepalives: make(map[string]time.Time),
	}
}

func (c *MemorySandboxCatalog) GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	_, span := tracer.Start(ctx, "sandbox-catalog-get")
	defer span.End()

	c.mtx.RLock()
	defer c.mtx.RUnlock()

	sandboxInfo := c.cache.Get(sandboxID)
	if sandboxInfo != nil {
		return sandboxInfo.Value(), nil
	}

	return nil, ErrSandboxNotFound
}

func (c *MemorySandboxCatalog) StoreSandbox(ctx context.Context, sandboxID string, sandboxInfo *SandboxInfo, expiration time.Duration) error {
	_, span := tracer.Start(ctx, "sandbox-catalog-store")
	defer span.End()

	logger.L().Debug(ctx, "storing sandbox in memory catalog", logger.WithSandboxID(sandboxID))

	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.cache.Set(sandboxID, sandboxInfo, expiration)

	return nil
}

func (c *MemorySandboxCatalog) AcquireTrafficKeepalive(_ context.Context, sandboxID string) (bool, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	now := time.Now()
	if expiresAt, ok := c.trafficKeepalives[sandboxID]; ok && now.Before(expiresAt) {
		return false, nil
	}

	c.trafficKeepalives[sandboxID] = now.Add(TrafficKeepaliveInterval)

	return true, nil
}

func (c *MemorySandboxCatalog) DeleteSandbox(ctx context.Context, sandboxID string, executionID string) error {
	_, span := tracer.Start(ctx, "sandbox-catalog-delete")
	defer span.End()

	c.mtx.Lock()
	defer c.mtx.Unlock()

	item := c.cache.Get(sandboxID)
	if item == nil {
		return nil
	}

	// No need for removal here
	if item.IsExpired() || item.Value() == nil {
		return nil
	}

	// Different execution is stored in the cache, we don't want to remove it
	if item.Value().ExecutionID != executionID {
		return nil
	}

	logger.L().Debug(ctx, "deleting sandbox from memory catalog", logger.WithSandboxID(sandboxID))
	c.cache.Delete(sandboxID)
	delete(c.trafficKeepalives, sandboxID)

	return nil
}

func (c *MemorySandboxCatalog) Close(_ context.Context) error {
	c.cache.Stop()

	return nil
}

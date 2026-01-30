package sandbox_catalog

import (
	"context"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type MemoryPausedSandboxCatalog struct {
	cache *ttlcache.Cache[string, *PausedSandboxInfo]
	mtx   sync.RWMutex
}

func NewMemoryPausedSandboxesCatalog() PausedSandboxesCatalog {
	cache := ttlcache.New(ttlcache.WithTTL[string, *PausedSandboxInfo](catalogMemoryLocalCacheTtl), ttlcache.WithDisableTouchOnHit[string, *PausedSandboxInfo]())
	go cache.Start()

	return &MemoryPausedSandboxCatalog{
		cache: cache,
	}
}

func (c *MemoryPausedSandboxCatalog) GetPaused(ctx context.Context, sandboxID string) (*PausedSandboxInfo, error) {
	_, span := tracer.Start(ctx, "paused-sandbox-catalog-get")
	defer span.End()

	c.mtx.RLock()
	defer c.mtx.RUnlock()

	item := c.cache.Get(sandboxID)
	if item != nil {
		return item.Value(), nil
	}

	return nil, ErrPausedSandboxNotFound
}

func (c *MemoryPausedSandboxCatalog) StorePaused(ctx context.Context, sandboxID string, info *PausedSandboxInfo, expiration time.Duration) error {
	_, span := tracer.Start(ctx, "paused-sandbox-catalog-store")
	defer span.End()

	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.cache.Set(sandboxID, info, expiration)

	return nil
}

func (c *MemoryPausedSandboxCatalog) DeletePaused(ctx context.Context, sandboxID string) error {
	_, span := tracer.Start(ctx, "paused-sandbox-catalog-delete")
	defer span.End()

	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.cache.Delete(sandboxID)

	return nil
}

func (c *MemoryPausedSandboxCatalog) Close(_ context.Context) error {
	c.cache.Stop()

	return nil
}

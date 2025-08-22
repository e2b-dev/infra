package sandboxes

import (
	"context"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/trace"
)

type MemorySandboxCatalog struct {
	cache  *ttlcache.Cache[string, *SandboxInfo]
	mtx    sync.RWMutex
	tracer trace.Tracer
}

func NewMemorySandboxesCatalog(tracer trace.Tracer) SandboxesCatalog {
	cache := ttlcache.New[string, *SandboxInfo](ttlcache.WithDisableTouchOnHit[string, *SandboxInfo]())
	go cache.Start()

	return &MemorySandboxCatalog{
		tracer: tracer,
		cache:  cache,
	}
}

func (c *MemorySandboxCatalog) GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	_, span := c.tracer.Start(ctx, "sandbox-catalog-get")
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
	_, span := c.tracer.Start(ctx, "sandbox-catalog-store")
	defer span.End()

	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.cache.Set(sandboxID, sandboxInfo, expiration)
	return nil
}

func (c *MemorySandboxCatalog) DeleteSandbox(ctx context.Context, sandboxID string, executionID string) error {
	_, span := c.tracer.Start(ctx, "sandbox-catalog-delete")
	defer span.End()

	c.mtx.Lock()
	defer c.mtx.Unlock()

	item := c.cache.Get(sandboxID)

	// No need for removal here
	if item.IsExpired() || item.Value() == nil {
		return nil
	}

	// Different execution is stored in the cache, we don't want to remove it
	if item.Value().ExecutionID != executionID {
		return nil
	}

	c.cache.Delete(sandboxID)
	return nil
}

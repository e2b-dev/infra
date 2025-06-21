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
	ctx    context.Context
	tracer trace.Tracer
}

func NewMemorySandboxesCatalog(ctx context.Context, tracer trace.Tracer) SandboxesCatalog {
	cache := ttlcache.New[string, *SandboxInfo](ttlcache.WithDisableTouchOnHit[string, *SandboxInfo]())
	go cache.Start()

	return &MemorySandboxCatalog{
		tracer: tracer,
		cache:  cache,
		ctx:    ctx,
	}
}

func (c *MemorySandboxCatalog) GetSandbox(sandboxId string) (*SandboxInfo, error) {
	_, span := c.tracer.Start(c.ctx, "sandbox-catalog-get")
	defer span.End()

	c.mtx.RLock()
	defer c.mtx.RUnlock()

	sandboxInfo := c.cache.Get(sandboxId)
	if sandboxInfo != nil {
		return sandboxInfo.Value(), nil
	}

	return nil, ErrSandboxNotFound
}

func (c *MemorySandboxCatalog) StoreSandbox(sandboxId string, sandboxInfo *SandboxInfo, expiration time.Duration) error {
	_, span := c.tracer.Start(c.ctx, "sandbox-catalog-store")
	defer span.End()

	c.mtx.Lock()
	defer c.mtx.Unlock()

	c.cache.Set(sandboxId, sandboxInfo, expiration)
	return nil
}

func (c *MemorySandboxCatalog) DeleteSandbox(sandboxId string, executionId string) error {
	_, span := c.tracer.Start(c.ctx, "sandbox-catalog-delete")
	defer span.End()

	c.mtx.Lock()
	defer c.mtx.Unlock()

	item := c.cache.Get(sandboxId)

	// No need for removal here
	if item.IsExpired() || item.Value() == nil {
		return nil
	}

	// Different execution is stored in the cache, we don't want to remove it
	if item.Value().ExecutionId != executionId {
		return nil
	}

	c.cache.Delete(sandboxId)
	return nil
}

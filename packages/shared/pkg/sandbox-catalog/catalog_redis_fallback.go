package sandbox_catalog

import (
	"context"
	"errors"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

type RedisFallbackSandboxCatalog struct {
	sandboxCatalog             SandboxesCatalog
	redisFallbackCatalogClient *RedisSandboxCatalog
	cache                      *ttlcache.Cache[string, *SandboxInfo]
}

var _ SandboxesCatalog = (*RedisFallbackSandboxCatalog)(nil)

func (r *RedisFallbackSandboxCatalog) GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	data, err := r.sandboxCatalog.GetSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			return r.redisFallbackCatalogClient.GetSandbox(ctx, sandboxID)
		}
	}

	return data, err
}

func (r *RedisFallbackSandboxCatalog) StoreSandbox(ctx context.Context, sandboxID string, sandboxInfo *SandboxInfo, expiration time.Duration) error {
	return r.sandboxCatalog.StoreSandbox(ctx, sandboxID, sandboxInfo, expiration)
}

func (r *RedisFallbackSandboxCatalog) DeleteSandbox(ctx context.Context, sandboxID string, executionID string) error {
	return r.sandboxCatalog.DeleteSandbox(ctx, sandboxID, executionID)
}

var _ SandboxesCatalog = (*RedisFallbackSandboxCatalog)(nil)

func NewRedisFallbackSandboxesCatalog(sandboxCatalog SandboxesCatalog, redisFallbackSandboxCatalog *RedisSandboxCatalog) *RedisFallbackSandboxCatalog {
	cache := ttlcache.New(ttlcache.WithTTL[string, *SandboxInfo](catalogRedisLocalCacheTtl), ttlcache.WithDisableTouchOnHit[string, *SandboxInfo]())
	go cache.Start()

	return &RedisFallbackSandboxCatalog{
		sandboxCatalog:             sandboxCatalog,
		redisFallbackCatalogClient: redisFallbackSandboxCatalog,
		cache:                      cache,
	}
}

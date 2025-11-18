package sandbox_catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	catalogRedisTimeout = time.Second * 1

	// this is just how long we are keeping sandbox in local cache so we don't have to query redis every time
	// we don't want to go too high because then sbx can be run on different orchestrator, and we will not be able to find it
	catalogRedisLocalCacheTtl = time.Millisecond * 500
)

type RedisSandboxCatalog struct {
	redisClient redis.UniversalClient
	cache       *ttlcache.Cache[string, *SandboxInfo]
}

var _ SandboxesCatalog = (*RedisSandboxCatalog)(nil)

func NewRedisSandboxesCatalog(redisClient redis.UniversalClient) *RedisSandboxCatalog {
	cache := ttlcache.New(ttlcache.WithTTL[string, *SandboxInfo](catalogRedisLocalCacheTtl), ttlcache.WithDisableTouchOnHit[string, *SandboxInfo]())
	go cache.Start()

	return &RedisSandboxCatalog{
		redisClient: redisClient,
		cache:       cache,
	}
}

var _ SandboxesCatalog = (*RedisSandboxCatalog)(nil)

func (c *RedisSandboxCatalog) GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	spanCtx, span := tracer.Start(ctx, "sandbox-catalog-get")
	defer span.End()

	sandboxInfo := c.cache.Get(sandboxID)
	if sandboxInfo != nil {
		return sandboxInfo.Value(), nil
	}

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	data, err := c.redisClient.Get(ctx, c.getCatalogKey(sandboxID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrSandboxNotFound
		}

		return nil, fmt.Errorf("failed to get sandbox info from redis: %w", err)
	}

	var info *SandboxInfo
	err = json.Unmarshal(data, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal sandbox info: %w", err)
	}

	// Store in local cache if needed
	c.cache.Set(sandboxID, info, catalogRedisLocalCacheTtl)

	return info, nil
}

func (c *RedisSandboxCatalog) StoreSandbox(ctx context.Context, sandboxID string, sandboxInfo *SandboxInfo, expiration time.Duration) error {
	spanCtx, span := tracer.Start(ctx, "sandbox-catalog-store")
	defer span.End()

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	bytes, err := json.Marshal(*sandboxInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox info: %w", err)
	}

	status := c.redisClient.Set(ctx, c.getCatalogKey(sandboxID), string(bytes), expiration)
	if status.Err() != nil {
		zap.L().Error("Error while storing sandbox in redis", logger.WithSandboxID(sandboxID), zap.Error(status.Err()))

		return fmt.Errorf("failed to store sandbox info in redis: %w", status.Err())
	}

	c.cache.Set(sandboxID, sandboxInfo, catalogRedisLocalCacheTtl)

	return nil
}

func (c *RedisSandboxCatalog) DeleteSandbox(ctx context.Context, sandboxID string, executionID string) error {
	spanCtx, span := tracer.Start(ctx, "sandbox-catalog-delete")
	defer span.End()

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	data, err := c.redisClient.Get(ctx, c.getCatalogKey(sandboxID)).Bytes()
	// If sandbox does not exist, we can return early
	if err != nil {
		return nil
	}

	var info *SandboxInfo
	err = json.Unmarshal(data, &info)
	if err != nil {
		return fmt.Errorf("failed to unmarshal sandbox info: %w", err)
	}

	// Different execution is stored in the cache, we don't want to remove it
	if info.ExecutionID != executionID {
		return nil
	}

	c.redisClient.Del(ctx, c.getCatalogKey(sandboxID))
	c.cache.Delete(sandboxID)

	return nil
}

func (c *RedisSandboxCatalog) getCatalogKey(sandboxID string) string {
	return fmt.Sprintf("sandbox:catalog:%s", sandboxID)
}

func (c *RedisSandboxCatalog) Close(_ context.Context) error {
	c.cache.Stop()

	return nil
}

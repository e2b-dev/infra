package sandbox_catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
)

const (
	pausedCatalogRedisTimeout       = time.Second * 1
	pausedCatalogRedisLocalCacheTtl = time.Millisecond * 500
)

type RedisPausedSandboxCatalog struct {
	redisClient redis.UniversalClient
	cache       *ttlcache.Cache[string, *PausedSandboxInfo]
}

var _ PausedSandboxesCatalog = (*RedisPausedSandboxCatalog)(nil)

func NewRedisPausedSandboxesCatalog(redisClient redis.UniversalClient) *RedisPausedSandboxCatalog {
	cache := ttlcache.New(ttlcache.WithTTL[string, *PausedSandboxInfo](pausedCatalogRedisLocalCacheTtl), ttlcache.WithDisableTouchOnHit[string, *PausedSandboxInfo]())
	go cache.Start()

	return &RedisPausedSandboxCatalog{
		redisClient: redisClient,
		cache:       cache,
	}
}

func (c *RedisPausedSandboxCatalog) GetPaused(ctx context.Context, sandboxID string) (*PausedSandboxInfo, error) {
	spanCtx, span := tracer.Start(ctx, "paused-sandbox-catalog-get")
	defer span.End()

	item := c.cache.Get(sandboxID)
	if item != nil {
		return item.Value(), nil
	}

	ctx, ctxCancel := context.WithTimeout(spanCtx, pausedCatalogRedisTimeout)
	defer ctxCancel()

	data, err := c.redisClient.Get(ctx, c.getCatalogKey(sandboxID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrPausedSandboxNotFound
		}

		return nil, fmt.Errorf("failed to get paused sandbox info from redis: %w", err)
	}

	var info *PausedSandboxInfo
	err = json.Unmarshal(data, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal paused sandbox info: %w", err)
	}

	c.cache.Set(sandboxID, info, pausedCatalogRedisLocalCacheTtl)

	return info, nil
}

func (c *RedisPausedSandboxCatalog) StorePaused(ctx context.Context, sandboxID string, info *PausedSandboxInfo, expiration time.Duration) error {
	spanCtx, span := tracer.Start(ctx, "paused-sandbox-catalog-store")
	defer span.End()

	ctx, ctxCancel := context.WithTimeout(spanCtx, pausedCatalogRedisTimeout)
	defer ctxCancel()

	bytes, err := json.Marshal(*info)
	if err != nil {
		return fmt.Errorf("failed to marshal paused sandbox info: %w", err)
	}

	status := c.redisClient.Set(ctx, c.getCatalogKey(sandboxID), string(bytes), expiration)
	if status.Err() != nil {
		return fmt.Errorf("failed to store paused sandbox info in redis: %w", status.Err())
	}

	c.cache.Set(sandboxID, info, pausedCatalogRedisLocalCacheTtl)

	return nil
}

func (c *RedisPausedSandboxCatalog) DeletePaused(ctx context.Context, sandboxID string) error {
	spanCtx, span := tracer.Start(ctx, "paused-sandbox-catalog-delete")
	defer span.End()

	ctx, ctxCancel := context.WithTimeout(spanCtx, pausedCatalogRedisTimeout)
	defer ctxCancel()

	c.redisClient.Del(ctx, c.getCatalogKey(sandboxID))
	c.cache.Delete(sandboxID)

	return nil
}

func (c *RedisPausedSandboxCatalog) getCatalogKey(sandboxID string) string {
	return fmt.Sprintf("sandbox:paused:%s", sandboxID)
}

func (c *RedisPausedSandboxCatalog) Close(_ context.Context) error {
	c.cache.Stop()

	return nil
}

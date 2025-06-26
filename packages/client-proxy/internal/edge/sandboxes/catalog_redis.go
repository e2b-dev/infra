package sandboxes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	catalogClusterLockName = "sandboxes-catalog-cluster-lock"
	catalogRedisTimeout    = time.Second * 5

	// this is just how long we are keeping sandbox in local cache so we don't have to query redis every time
	// we don't want to go too high because then sbx can be run on different orchestrator, and we will not be able to find it
	catalogRedisLocalCacheTtl = time.Second * 5
)

type RedisSandboxCatalog struct {
	// todo: ideally we want to support per sandbox locking, but for now we are using one global lock per cluster
	clusterMutex *redsync.Mutex
	redisClient  redis.UniversalClient

	cache  *ttlcache.Cache[string, *SandboxInfo]
	ctx    context.Context
	tracer trace.Tracer
}

func NewRedisSandboxesCatalog(ctx context.Context, tracer trace.Tracer, redisClient redis.UniversalClient, redisSync *redsync.Redsync) SandboxesCatalog {
	clusterLockMutex := redisSync.NewMutex(catalogClusterLockName)

	cache := ttlcache.New(ttlcache.WithTTL[string, *SandboxInfo](catalogRedisLocalCacheTtl), ttlcache.WithDisableTouchOnHit[string, *SandboxInfo]())
	go cache.Start()

	return &RedisSandboxCatalog{
		redisClient:  redisClient,
		clusterMutex: clusterLockMutex,
		tracer:       tracer,
		cache:        cache,
		ctx:          ctx,
	}
}

func (c *RedisSandboxCatalog) GetSandbox(sandboxId string) (*SandboxInfo, error) {
	spanCtx, span := c.tracer.Start(c.ctx, "sandbox-catalog-get")
	defer span.End()

	sandboxInfo := c.cache.Get(sandboxId)
	if sandboxInfo != nil {
		return sandboxInfo.Value(), nil
	}

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	data, err := c.redisClient.Get(ctx, c.getCatalogKey(sandboxId)).Bytes()
	if err != nil {
		return nil, ErrSandboxNotFound
	}

	var info *SandboxInfo
	err = json.Unmarshal(data, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal sandbox info: %w", err)
	}

	deadline := info.SandboxStartedAt.
		Add(time.Duration(info.SandboxMaxLengthInHours) * time.Hour).
		Add(sandboxTtlBuffer)

	err = c.StoreSandbox(sandboxId, info, time.Until(deadline))
	if err != nil {
		return nil, fmt.Errorf("failed to store sandbox info taken from redis: %w", err)
	}

	return info, nil
}

func (c *RedisSandboxCatalog) StoreSandbox(sandboxId string, sandboxInfo *SandboxInfo, expiration time.Duration) error {
	spanCtx, span := c.tracer.Start(c.ctx, "sandbox-catalog-store")
	defer span.End()

	err := c.clusterMutex.Lock()
	if err != nil {
		return fmt.Errorf("error while locking the cluster mutex: %w", err)
	}

	defer c.clusterMutex.Unlock()

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	bytes, err := json.Marshal(*sandboxInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox info: %w", err)
	}

	status := c.redisClient.Set(ctx, c.getCatalogKey(sandboxId), string(bytes), expiration)
	if status.Err() != nil {
		zap.L().Error("Error while storing sandbox in redis", logger.WithSandboxID(sandboxId), zap.Error(status.Err()))
		return fmt.Errorf("failed to store sandbox info in redis: %w", status.Err())
	}

	c.cache.Set(sandboxId, sandboxInfo, catalogRedisLocalCacheTtl)

	return nil
}

func (c *RedisSandboxCatalog) DeleteSandbox(sandboxId string, executionId string) error {
	spanCtx, span := c.tracer.Start(c.ctx, "sandbox-catalog-delete")
	defer span.End()

	err := c.clusterMutex.Lock()
	if err != nil {
		return fmt.Errorf("error while locking the cluster mutex: %w", err)
	}

	defer c.clusterMutex.Unlock()

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	data, err := c.redisClient.Get(ctx, c.getCatalogKey(sandboxId)).Bytes()
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
	if info.ExecutionId != executionId {
		return nil
	}

	c.redisClient.Del(ctx, c.getCatalogKey(sandboxId))
	c.cache.Delete(sandboxId)
	return nil
}

func (c *RedisSandboxCatalog) getCatalogKey(sandboxId string) string {
	return fmt.Sprintf("sandbox-%s", sandboxId)
}

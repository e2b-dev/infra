package sandbox_catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	catalogRedisTimeout = time.Second * 1
)

type RedisSandboxCatalog struct {
	redisClient redis.UniversalClient
}

var _ SandboxesCatalog = (*RedisSandboxCatalog)(nil)

func NewRedisSandboxCatalog(redisClient redis.UniversalClient) *RedisSandboxCatalog {
	return &RedisSandboxCatalog{
		redisClient: redisClient,
	}
}

func (c *RedisSandboxCatalog) GetSandbox(ctx context.Context, sandboxID string) (*SandboxInfo, error) {
	spanCtx, span := tracer.Start(ctx, "sandbox-catalog-get")
	defer span.End()

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

	return info, nil
}

func (c *RedisSandboxCatalog) StoreSandbox(ctx context.Context, sandboxID string, sandboxInfo *SandboxInfo, expiration time.Duration) error {
	spanCtx, span := tracer.Start(ctx, "sandbox-catalog-store")
	defer span.End()

	logger.L().Debug(ctx, "storing sandbox in redis catalog", logger.WithSandboxID(sandboxID))

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	bytes, err := json.Marshal(*sandboxInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox info: %w", err)
	}

	status := c.redisClient.Set(ctx, c.getCatalogKey(sandboxID), string(bytes), expiration)
	if status.Err() != nil {
		logger.L().Error(ctx, "Error while storing sandbox in redis", logger.WithSandboxID(sandboxID), zap.Error(status.Err()))

		return fmt.Errorf("failed to store sandbox info in redis: %w", status.Err())
	}

	return nil
}

func (c *RedisSandboxCatalog) AcquireTrafficKeepalive(ctx context.Context, sandboxID string) (bool, error) {
	spanCtx, span := tracer.Start(ctx, "sandbox-catalog-traffic-keepalive-acquire")
	defer span.End()

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	acquired, err := c.redisClient.SetNX(ctx, c.getTrafficKeepaliveKey(sandboxID), "1", TrafficKeepaliveInterval).Result()
	if err != nil {
		return false, fmt.Errorf("failed to acquire traffic keepalive semaphore: %w", err)
	}

	return acquired, nil
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

	logger.L().Debug(ctx, "deleting sandbox from redis catalog", logger.WithSandboxID(sandboxID))
	c.redisClient.Del(ctx, c.getCatalogKey(sandboxID))
	c.redisClient.Del(ctx, c.getTrafficKeepaliveKey(sandboxID))

	return nil
}

func (c *RedisSandboxCatalog) getCatalogKey(sandboxID string) string {
	return fmt.Sprintf("sandbox:catalog:%s", sandboxID)
}

func (c *RedisSandboxCatalog) getTrafficKeepaliveKey(sandboxID string) string {
	return fmt.Sprintf("sandbox:catalog:%s:traffic-keepalive", sandboxID)
}

func (c *RedisSandboxCatalog) Close(_ context.Context) error {
	return nil
}

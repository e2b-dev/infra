package sandboxes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
)

type SandboxInfo struct {
	OrchestratorId string `json:"orchestrator_id"`
	TemplateId     string `json:"template_id"`

	//StartedAt time.Time
}

type SandboxesCatalog struct {
	clusterMutex *redsync.Mutex
	redisClient  *redis.Client

	cache  *ttlcache.Cache[string, *SandboxInfo]
	ctx    context.Context
	tracer trace.Tracer
}

const (
	catalogClusterLockName = "sandboxes-catalog-cluster-lock"
	catalogCacheExpiration = time.Minute * 5
	catalogRedisTimeout    = time.Second * 5
)

var (
	SandboxNotFoundError = errors.New("sandbox not found")
)

func NewSandboxesCatalog(ctx context.Context, redisClient *redis.Client, redisSync *redsync.Redsync, tracer trace.Tracer) *SandboxesCatalog {
	clusterLockMutex := redisSync.NewMutex(catalogClusterLockName)

	cache := ttlcache.New(ttlcache.WithTTL[string, *SandboxInfo](catalogCacheExpiration))
	go cache.Start()

	return &SandboxesCatalog{
		redisClient:  redisClient,
		clusterMutex: clusterLockMutex,
		tracer:       tracer,
		cache:        cache,
		ctx:          ctx,
	}
}

func (c *SandboxesCatalog) GetSandbox(sandboxId string) (*SandboxInfo, error) {
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
		return nil, SandboxNotFoundError
	}

	var info *SandboxInfo
	err = json.Unmarshal(data, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal sandbox info: %w", err)
	}

	err = c.StoreSandbox(sandboxId, info)
	if err != nil {
		return nil, fmt.Errorf("failed to store sandbox info taken from redis: %w", err)
	}

	return info, nil
}

func (c *SandboxesCatalog) StoreSandbox(sandboxId string, sandboxInfo *SandboxInfo) error {
	spanCtx, span := c.tracer.Start(c.ctx, "sandbox-catalog-store")
	defer span.End()

	err := c.clusterMutex.Lock()
	if err != nil {
		return fmt.Errorf("error while locking the cluster mutex: %w", err)
	}

	defer c.clusterMutex.Unlock()

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	c.redisClient.Set(ctx, c.getCatalogKey(sandboxId), sandboxInfo, catalogCacheExpiration)
	c.cache.Set(sandboxId, sandboxInfo, catalogCacheExpiration)

	return nil
}

func (c *SandboxesCatalog) DeleteSandbox(sandboxId string) error {
	spanCtx, span := c.tracer.Start(c.ctx, "sandbox-catalog-delete")
	defer span.End()

	err := c.clusterMutex.Lock()
	if err != nil {
		return fmt.Errorf("error while locking the cluster mutex: %w", err)
	}

	defer c.clusterMutex.Unlock()

	ctx, ctxCancel := context.WithTimeout(spanCtx, catalogRedisTimeout)
	defer ctxCancel()

	c.redisClient.Del(ctx, c.getCatalogKey(sandboxId))
	c.cache.Delete(sandboxId)

	return nil
}

func (c *SandboxesCatalog) getCatalogKey(sandboxId string) string {
	return fmt.Sprintf("sandbox-%s", sandboxId)
}

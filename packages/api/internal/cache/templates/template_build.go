package templatecache

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// TemplateBuildInfo holds cached template build information.
type TemplateBuildInfo struct {
	TeamID      uuid.UUID         `json:"team_id"`
	TemplateID  string            `json:"template_id"`
	BuildStatus types.BuildStatusGroup `json:"build_status"`
	Reason      types.BuildReason `json:"reason"`
	Version     *string           `json:"version,omitempty"`

	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    *string   `json:"node_id,omitempty"`
}

type TemplateBuildInfoNotFoundError struct{}

func (TemplateBuildInfoNotFoundError) Error() string {
	return "Template build info not found"
}

type TemplatesBuildCache struct {
	l1Cache     *ttlcache.Cache[uuid.UUID, TemplateBuildInfo]
	redisClient redis.UniversalClient
	db          *sqlcdb.Client
}

func NewTemplateBuildCache(db *sqlcdb.Client, redisClient redis.UniversalClient) *TemplatesBuildCache {
	l1Cache := ttlcache.New(
		ttlcache.WithTTL[uuid.UUID, TemplateBuildInfo](l1CacheTTL),
		ttlcache.WithDisableTouchOnHit[uuid.UUID, TemplateBuildInfo](),
	)
	go l1Cache.Start()

	return &TemplatesBuildCache{
		l1Cache:     l1Cache,
		redisClient: redisClient,
		db:          db,
	}
}

func (c *TemplatesBuildCache) SetStatus(ctx context.Context, buildID uuid.UUID, status types.BuildStatus, reason types.BuildReason) {
	// Get existing build info for logging (optional)
	var fromStatus string
	var version *string
	if item := c.l1Cache.Get(buildID); item != nil {
		existingInfo := item.Value()
		fromStatus = string(existingInfo.BuildStatus)
		version = existingInfo.Version
	}

	logger.L().Info(ctx, "Setting template build status",
		logger.WithBuildID(buildID.String()),
		zap.String("to_status", string(status)),
		zap.String("from_status", fromStatus),
		zap.String("reason", reason.Message),
		zap.String("step", sharedUtils.Sprintp(reason.Step)),
		zap.String("version", sharedUtils.Sprintp(version)),
	)

	// Update in Redis
	if err := c.updateStatusInRedis(ctx, buildID, status, reason); err != nil {
		logger.L().Warn(ctx, "Failed to update build status in Redis",
			logger.WithBuildID(buildID.String()),
			zap.Error(err))
	}

	// Invalidate L1 cache entry to force re-fetch from Redis on next read
	c.l1Cache.Delete(buildID)
}

func (c *TemplatesBuildCache) Get(ctx context.Context, buildID uuid.UUID, templateID string) (TemplateBuildInfo, error) {
	// Step 1: Check L1 cache
	if item := c.l1Cache.Get(buildID); item != nil {
		return item.Value(), nil
	}

	// Step 2: Check L2 (Redis)
	info, err := c.getFromRedis(ctx, buildID)
	if err == nil {
		// Store in L1 cache
		c.l1Cache.Set(buildID, info, l1CacheTTL)

		return info, nil
	}

	// If Redis error is not "not found", log it but continue to DB
	if !errors.Is(err, redis.Nil) {
		logger.L().Warn(ctx, "Redis error while getting build, falling back to DB",
			logger.WithBuildID(buildID.String()),
			zap.Error(err))
	}

	// Step 3: Fetch from DB
	logger.L().Debug(ctx, "Template build info not found in cache, fetching from DB",
		logger.WithBuildID(buildID.String()))

	info, err = c.fetchFromDB(ctx, buildID, templateID)
	if err != nil {
		return TemplateBuildInfo{}, err
	}

	// Store in both L1 and L2 (Redis)
	c.l1Cache.Set(buildID, info, l1CacheTTL)
	if storeErr := c.storeInRedis(ctx, buildID, info); storeErr != nil {
		logger.L().Warn(ctx, "Failed to store build info in Redis",
			logger.WithBuildID(buildID.String()),
			zap.Error(storeErr))
	}

	return info, nil
}

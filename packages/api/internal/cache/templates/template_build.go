package templatecache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	buildCacheTTL             = 5 * time.Minute
	buildCacheRefreshInterval = 1 * time.Minute
	buildCacheTimeout         = 2 * time.Second

	buildCacheKeyPrefix = "template:build"
)

type TemplateBuildInfo struct {
	TeamID      uuid.UUID              `json:"team_id"`
	TemplateID  string                 `json:"template_id"`
	BuildStatus types.BuildStatusGroup `json:"build_status"`
	Reason      types.BuildReason      `json:"reason"`
	Version     *string                `json:"version,omitempty"`

	ClusterID uuid.UUID `json:"cluster_id"`
	NodeID    *string   `json:"node_id,omitempty"`
}

var ErrTemplateBuildInfoNotFound = errors.New("template build info not found")

// Lua script to atomically update build status.
// Keys: [buildKey]
// Args: [newStatus, newReasonJSON, ttlSeconds]
// Returns: updated JSON or nil if key doesn't exist
var updateStatusScript = redis.NewScript(`
local buildKey = KEYS[1]
local newStatus = ARGV[1]
local newReasonJSON = ARGV[2]
local ttl = tonumber(ARGV[3])

local data = redis.call('GET', buildKey)
if not data then
    return nil
end

local build = cjson.decode(data)
build.build_status = newStatus
build.reason = cjson.decode(newReasonJSON)

local encoded = cjson.encode(build)
redis.call('SET', buildKey, encoded, 'EX', ttl)

return encoded
`)

type TemplatesBuildCache struct {
	cache *cache.RedisCache[TemplateBuildInfo]
	db    *sqlcdb.Client
}

func NewTemplateBuildCache(db *sqlcdb.Client, redisClient redis.UniversalClient) *TemplatesBuildCache {
	rc := cache.NewRedisCache[TemplateBuildInfo](cache.RedisConfig[TemplateBuildInfo]{
		TTL:             buildCacheTTL,
		RefreshInterval: buildCacheRefreshInterval,
		RedisTimeout:    buildCacheTimeout,
		RedisClient:     redisClient,
		RedisPrefix:     buildCacheKeyPrefix,
	})

	return &TemplatesBuildCache{
		cache: rc,
		db:    db,
	}
}

func (c *TemplatesBuildCache) SetStatus(ctx context.Context, buildID uuid.UUID, status types.BuildStatusGroup, reason types.BuildReason) {
	// Update in Redis using Lua script
	if err := c.updateStatusInRedis(ctx, buildID, status, reason); err != nil {
		logger.L().Warn(ctx, "Failed to update build status in Redis",
			logger.WithBuildID(buildID.String()),
			zap.Error(err))
	}
}

func (c *TemplatesBuildCache) Get(ctx context.Context, buildID uuid.UUID, templateID string) (TemplateBuildInfo, error) {
	return c.cache.GetOrSet(ctx, buildID.String(), c.fetchFromDB(templateID, buildID))
}

func (c *TemplatesBuildCache) Close(ctx context.Context) error {
	return c.cache.Close(ctx)
}

// fetchFromDB returns a callback that fetches the build from the database.
func (c *TemplatesBuildCache) fetchFromDB(templateID string, buildID uuid.UUID) func(context.Context, string) (TemplateBuildInfo, error) {
	return func(ctx context.Context, _ string) (TemplateBuildInfo, error) {
		result, err := c.db.GetTemplateBuildWithTemplate(ctx, queries.GetTemplateBuildWithTemplateParams{
			TemplateID: templateID,
			BuildID:    buildID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return TemplateBuildInfo{}, ErrTemplateBuildInfoNotFound
			}

			return TemplateBuildInfo{}, fmt.Errorf("failed to get template build '%s': %w", buildID, err)
		}

		return TemplateBuildInfo{
			TeamID:      result.Env.TeamID,
			TemplateID:  result.Env.ID,
			BuildStatus: result.EnvBuild.StatusGroup,
			Reason:      result.EnvBuild.Reason,
			Version:     result.EnvBuild.Version,
			ClusterID:   clusters.WithClusterFallback(result.Env.ClusterID),
			NodeID:      result.EnvBuild.ClusterNodeID,
		}, nil
	}
}

// updateStatusInRedis atomically updates the build status in Redis using a Lua script.
func (c *TemplatesBuildCache) updateStatusInRedis(ctx context.Context, buildID uuid.UUID, status types.BuildStatusGroup, reason types.BuildReason) error {
	ctx, cancel := context.WithTimeout(ctx, buildCacheTimeout)
	defer cancel()

	reasonJSON, err := json.Marshal(reason)
	if err != nil {
		return fmt.Errorf("failed to marshal reason: %w", err)
	}

	buildKey := c.cache.RedisKey(buildID.String())

	ttlSeconds := int(buildCacheTTL.Seconds())
	_, err = updateStatusScript.Run(ctx, c.cache.RedisClient(), []string{buildKey}, string(status), string(reasonJSON), ttlSeconds).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("failed to update status in Redis: %w", err)
	}

	return nil
}

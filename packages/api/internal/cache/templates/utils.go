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

	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
)

const (
	// l1CacheTTL is the in-memory cache TTL for fast local reads.
	l1CacheTTL = 1 * time.Second

	// redisBuildCacheTTL is the Redis cache TTL for build info.
	redisBuildCacheTTL = 5 * time.Minute

	// redisBuildCacheTimeout is the timeout for Redis operations.
	redisBuildCacheTimeout = 5 * time.Second

	// Key prefix for build cache.
	buildCacheKeyPrefix = "template:build"
)

// Lua script to atomically update build status.
// Keys: [buildKey]
// Args: [newStatus, newReasonJSON]
// Returns: updated JSON or nil if key doesn't exist
var updateStatusScript = redis.NewScript(`
local buildKey = KEYS[1]
local newStatus = ARGV[1]
local newReasonJSON = ARGV[2]

local data = redis.call('GET', buildKey)
if not data then
    return nil
end

local build = cjson.decode(data)
build.build_status = newStatus
build.reason = cjson.decode(newReasonJSON)

local encoded = cjson.encode(build)
redis.call('SET', buildKey, encoded, 'KEEPTTL')

return encoded
`)

// getBuildKey returns the Redis key for a build.
// Format: template:build:{buildID}
func getBuildKey(buildID string) string {
	return fmt.Sprintf("%s:%s", buildCacheKeyPrefix, buildID)
}

// getFromRedis retrieves a build from Redis.
func (c *TemplatesBuildCache) getFromRedis(ctx context.Context, buildID uuid.UUID) (TemplateBuildInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, redisBuildCacheTimeout)
	defer cancel()

	buildKey := getBuildKey(buildID.String())
	data, err := c.redisClient.Get(ctx, buildKey).Bytes()
	if err != nil {
		return TemplateBuildInfo{}, err
	}

	var info TemplateBuildInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return TemplateBuildInfo{}, fmt.Errorf("failed to unmarshal build info: %w", err)
	}

	return info, nil
}

// storeInRedis stores a build in Redis.
func (c *TemplatesBuildCache) storeInRedis(ctx context.Context, buildID uuid.UUID, info TemplateBuildInfo) error {
	ctx, cancel := context.WithTimeout(ctx, redisBuildCacheTimeout)
	defer cancel()

	buildJSON, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal build info: %w", err)
	}

	buildKey := getBuildKey(buildID.String())

	return c.redisClient.Set(ctx, buildKey, buildJSON, redisBuildCacheTTL).Err()
}

// fetchFromDB fetches a build from the database.
func (c *TemplatesBuildCache) fetchFromDB(ctx context.Context, buildID uuid.UUID, templateID string) (TemplateBuildInfo, error) {
	result, err := c.db.GetTemplateBuildWithTemplate(ctx, queries.GetTemplateBuildWithTemplateParams{
		TemplateID: templateID,
		BuildID:    buildID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TemplateBuildInfo{}, TemplateBuildInfoNotFoundError{}
		}

		return TemplateBuildInfo{}, fmt.Errorf("failed to get template build '%s': %w", buildID, err)
	}

	return TemplateBuildInfo{
		TeamID:      result.Env.TeamID,
		TemplateID:  result.Env.ID,
		BuildStatus: types.BuildStatus(result.EnvBuild.Status),
		Reason:      result.EnvBuild.Reason,
		Version:     result.EnvBuild.Version,
		ClusterID:   utils.WithClusterFallback(result.Env.ClusterID),
		NodeID:      result.EnvBuild.ClusterNodeID,
	}, nil
}

// updateStatusInRedis atomically updates the build status in Redis using a Lua script.
func (c *TemplatesBuildCache) updateStatusInRedis(ctx context.Context, buildID uuid.UUID, status types.BuildStatus, reason types.BuildReason) error {
	ctx, cancel := context.WithTimeout(ctx, redisBuildCacheTimeout)
	defer cancel()

	reasonJSON, err := json.Marshal(reason)
	if err != nil {
		return fmt.Errorf("failed to marshal reason: %w", err)
	}

	buildKey := getBuildKey(buildID.String())

	_, err = updateStatusScript.Run(ctx, c.redisClient, []string{buildKey}, string(status), string(reasonJSON)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("failed to update status in Redis: %w", err)
	}

	return nil
}

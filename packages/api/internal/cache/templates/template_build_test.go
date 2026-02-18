package templatecache

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// TestRedisTemplatesBuildCache_Get_CacheHit tests that Get returns from Redis cache when available.
func TestRedisTemplatesBuildCache_Get_CacheHit(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	c := NewTemplateBuildCache(db.SqlcClient, redisClient)

	buildID := uuid.New()
	info := TemplateBuildInfo{
		TeamID:      uuid.New(),
		TemplateID:  "test-template",
		BuildStatus: types.BuildStatusGroupInProgress,
		ClusterID:   uuid.New(),
	}

	// Store in Redis via the cache's Set
	c.cache.Set(ctx, buildID.String(), info)

	// Get should return from Redis without hitting DB
	result, err := c.Get(ctx, buildID, "test-template")
	require.NoError(t, err)
	assert.Equal(t, info.TeamID, result.TeamID)
	assert.Equal(t, info.TemplateID, result.TemplateID)
	assert.Equal(t, info.BuildStatus, result.BuildStatus)
}

// TestRedisTemplatesBuildCache_Get_L2Hit tests that storing in Redis works and Get retrieves from Redis.
func TestRedisTemplatesBuildCache_Get_L2Hit(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	c := NewTemplateBuildCache(db.SqlcClient, redisClient)

	buildID := uuid.New()
	info := TemplateBuildInfo{
		TeamID:      uuid.New(),
		TemplateID:  "test-template",
		BuildStatus: types.BuildStatusGroupInProgress,
		ClusterID:   uuid.New(),
	}

	// Store directly in Redis
	buildJSON, err := json.Marshal(info)
	require.NoError(t, err)

	buildKey := c.cache.RedisKey(buildID.String())
	err = redisClient.Set(ctx, buildKey, buildJSON, buildCacheTTL).Err()
	require.NoError(t, err)

	// Get should return from Redis
	result, err := c.Get(ctx, buildID, "test-template")
	require.NoError(t, err)
	assert.Equal(t, info.TeamID, result.TeamID)
	assert.Equal(t, info.TemplateID, result.TemplateID)
}

// TestRedisTemplatesBuildCache_Get_DBFallback tests that Get falls back to DB when cache misses.
func TestRedisTemplatesBuildCache_Get_DBFallback(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	// Create test data in DB
	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "building")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	c := NewTemplateBuildCache(db.SqlcClient, redisClient)
	defer c.Close(t.Context())

	// Get should fall back to DB
	result, err := c.Get(ctx, buildID, templateID)
	require.NoError(t, err)
	assert.Equal(t, teamID, result.TeamID)
	assert.Equal(t, templateID, result.TemplateID)
}

// TestRedisTemplatesBuildCache_Get_NotFound tests that Get returns error when build not found.
func TestRedisTemplatesBuildCache_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	c := NewTemplateBuildCache(db.SqlcClient, redisClient)
	defer c.Close(t.Context())

	// Try to get non-existent build
	_, err := c.Get(ctx, uuid.New(), "non-existent-template")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTemplateBuildInfoNotFound)
}

// TestRedisTemplatesBuildCache_SetStatus_UpdatesRedis tests that SetStatus updates Redis.
func TestRedisTemplatesBuildCache_SetStatus_UpdatesRedis(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	c := NewTemplateBuildCache(db.SqlcClient, redisClient)
	defer c.Close(t.Context())

	buildID := uuid.New()
	info := TemplateBuildInfo{
		TeamID:      uuid.New(),
		TemplateID:  "test-template",
		BuildStatus: types.BuildStatusGroupInProgress,
		ClusterID:   uuid.New(),
	}

	// Store in Redis via the cache
	c.cache.Set(ctx, buildID.String(), info)

	buildKey := c.cache.RedisKey(buildID.String())

	// Update status
	newReason := types.BuildReason{Message: "Build completed successfully"}
	c.SetStatus(ctx, buildID, types.BuildStatusGroupReady, newReason)

	// Redis should be updated
	data, err := redisClient.Get(ctx, buildKey).Bytes()
	require.NoError(t, err)

	var updatedInfo TemplateBuildInfo
	err = json.Unmarshal(data, &updatedInfo)
	require.NoError(t, err)
	assert.Equal(t, types.BuildStatusGroupReady, updatedInfo.BuildStatus)
	assert.Equal(t, "Build completed successfully", updatedInfo.Reason.Message)
}

// TestRedisTemplatesBuildCache_SetStatus_ResetsTTL tests that SetStatus resets the Redis TTL
func TestRedisTemplatesBuildCache_SetStatus_ResetsTTL(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	c := NewTemplateBuildCache(db.SqlcClient, redisClient)
	defer c.Close(t.Context())

	buildID := uuid.New()
	info := TemplateBuildInfo{
		TeamID:      uuid.New(),
		TemplateID:  "test-template",
		BuildStatus: types.BuildStatusGroupPending,
		ClusterID:   uuid.New(),
	}

	// Store in Redis with a short TTL to simulate an aging entry
	buildJSON, err := json.Marshal(info)
	require.NoError(t, err)

	buildKey := c.cache.RedisKey(buildID.String())
	shortTTL := 30 * time.Second
	err = redisClient.Set(ctx, buildKey, buildJSON, shortTTL).Err()
	require.NoError(t, err)

	// Verify the initial TTL is short
	ttlBefore, err := redisClient.TTL(ctx, buildKey).Result()
	require.NoError(t, err)
	assert.LessOrEqual(t, ttlBefore, shortTTL)

	// Update status — this should reset TTL
	newReason := types.BuildReason{Message: "Build started"}
	c.SetStatus(ctx, buildID, types.BuildStatusGroupInProgress, newReason)

	// Verify TTL was reset
	ttlAfter, err := redisClient.TTL(ctx, buildKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttlAfter, shortTTL, "TTL should be reset, now being less than initial short TTL")
	assert.LessOrEqual(t, ttlAfter, buildCacheTTL)
}

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

// TestRedisTemplatesBuildCache_Get_L1Hit tests that Get returns from L1 cache when available.
func TestRedisTemplatesBuildCache_Get_L1Hit(t *testing.T) {
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

	// Store directly in L1 cache via the layered cache's Set (populates both L1 and Redis)
	c.cache.Set(ctx, buildID.String(), info)

	// Get should return from L1 without hitting DB
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
	err = redisClient.Set(ctx, buildKey, buildJSON, redisBuildCacheTTL).Err()
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

// TestRedisTemplatesBuildCache_SetStatus_UpdatesAndInvalidatesL1 tests that SetStatus updates Redis and invalidates L1.
func TestRedisTemplatesBuildCache_SetStatus_UpdatesAndInvalidatesL1(t *testing.T) {
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

	// Store in both L1 and Redis via the layered cache
	c.cache.Set(ctx, buildID.String(), info)

	buildKey := c.cache.RedisKey(buildID.String())

	// Update status
	newReason := types.BuildReason{Message: "Build completed successfully"}
	c.SetStatus(ctx, buildID, types.BuildStatusGroupReady, newReason)

	// L1 should be invalidated
	_, ok := c.cache.GetWithoutTouch(buildID.String())
	assert.False(t, ok)

	// Redis should be updated
	data, err := redisClient.Get(ctx, buildKey).Bytes()
	require.NoError(t, err)

	var updatedInfo TemplateBuildInfo
	err = json.Unmarshal(data, &updatedInfo)
	require.NoError(t, err)
	assert.Equal(t, types.BuildStatusGroupReady, updatedInfo.BuildStatus)
	assert.Equal(t, "Build completed successfully", updatedInfo.Reason.Message)
}

// TestRedisTemplatesBuildCache_SetStatus_ResetsTTL tests that SetStatus resets the Redis TTL to redisBuildCacheTTL.
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

	// Update status — this should reset TTL to redisBuildCacheTTL (5 minutes)
	newReason := types.BuildReason{Message: "Build started"}
	c.SetStatus(ctx, buildID, types.BuildStatusGroupInProgress, newReason)

	// Verify TTL was reset to redisBuildCacheTTL
	ttlAfter, err := redisClient.TTL(ctx, buildKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttlAfter, shortTTL, "TTL should be reset to redisBuildCacheTTL, not the old short TTL")
	assert.LessOrEqual(t, ttlAfter, redisBuildCacheTTL)
}

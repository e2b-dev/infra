package templatecache

import (
	"encoding/json"
	"testing"

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

	cache := NewTemplateBuildCache(db.SqlcClient, redisClient)

	buildID := uuid.New()
	info := TemplateBuildInfo{
		TeamID:      uuid.New(),
		TemplateID:  "test-template",
		BuildStatus: types.BuildStatusGroupInProgress,
		ClusterID:   uuid.New(),
	}

	// Store directly in L1 cache
	cache.l1Cache.Set(buildID.String(), info)

	// Get should return from L1 without hitting Redis or DB
	result, err := cache.Get(ctx, buildID, "test-template")
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

	cache := NewTemplateBuildCache(db.SqlcClient, redisClient)

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

	buildKey := getBuildKey(buildID.String())
	err = redisClient.Set(ctx, buildKey, buildJSON, redisBuildCacheTTL).Err()
	require.NoError(t, err)

	// Get should return from Redis
	result, err := cache.Get(ctx, buildID, "test-template")
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

	cache := NewTemplateBuildCache(db.SqlcClient, redisClient)
	defer cache.Close(t.Context())

	// Get should fall back to DB
	result, err := cache.Get(ctx, buildID, templateID)
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

	cache := NewTemplateBuildCache(db.SqlcClient, redisClient)
	defer cache.Close(t.Context())

	// Try to get non-existent build
	_, err := cache.Get(ctx, uuid.New(), "non-existent-template")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTemplateBuildInfoNotFound)
}

// TestRedisTemplatesBuildCache_SetStatus_UpdatesAndInvalidatesL1 tests that SetStatus updates Redis and invalidates L1.
func TestRedisTemplatesBuildCache_SetStatus_UpdatesAndInvalidatesL1(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	cache := NewTemplateBuildCache(db.SqlcClient, redisClient)
	defer cache.Close(t.Context())

	buildID := uuid.New()
	info := TemplateBuildInfo{
		TeamID:      uuid.New(),
		TemplateID:  "test-template",
		BuildStatus: types.BuildStatusGroupInProgress,
		ClusterID:   uuid.New(),
	}

	// Store in L1 and Redis
	cache.l1Cache.Set(buildID.String(), info)
	buildJSON, err := json.Marshal(info)
	require.NoError(t, err)

	buildKey := getBuildKey(buildID.String())
	err = redisClient.Set(ctx, buildKey, string(buildJSON), redisBuildCacheTTL).Err()
	require.NoError(t, err)

	// Update status
	newReason := types.BuildReason{Message: "Build completed successfully"}
	cache.SetStatus(ctx, buildID, types.BuildStatusGroupReady, newReason)

	// L1 should be invalidated
	_, ok := cache.l1Cache.GetWithoutTouch(buildID.String())
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

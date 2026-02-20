package templatecache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// TestTemplateCache_InvalidateDoesNotInvalidateAliases tests that TemplateCache.Invalidate
// does NOT invalidate alias cache entries (only InvalidateAllTags does metadata)
func TestTemplateCache_InvalidateDoesNotInvalidateAliases(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "alias-for-template", &teamSlug)

	cache := NewTemplateCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Resolve alias to populate alias cache
	resolvedID, err := cache.ResolveAlias(ctx, "alias-for-template", teamSlug)
	require.NoError(t, err)
	assert.Equal(t, templateID, resolvedID)

	// Verify alias key exists in Redis
	aliasKey := cache.aliasCache.cache.RedisKey(buildAliasKey(&teamSlug, "alias-for-template"))
	exists, err := redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "alias key should exist before template invalidation")

	// Invalidate the template (should NOT invalidate alias cache)
	cache.Invalidate(ctx, templateID, nil)

	// Alias key should still exist in Redis
	exists, err = redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "alias key should survive template invalidation")
}

// TestTemplateCache_InvalidateAllTagsDeletesRedisEntries tests that
// InvalidateAllTags deletes Redis entries.
func TestTemplateCache_InvalidateAllTagsDeletesRedisEntries(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	tc := NewTemplateCache(db.SqlcClient, redisClient)
	defer tc.Close(ctx)

	// Populate the cache (this backfills into Redis via the callback)
	_, _, err := tc.Get(ctx, templateID, nil, teamID, consts.LocalClusterID)
	require.NoError(t, err)

	// Verify the entry exists in Redis
	cacheKey := buildCacheKey(templateID, "default")
	redisKey := tc.cache.RedisKey(cacheKey)
	exists, err := redisClient.Exists(ctx, redisKey).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), exists)

	// InvalidateAllTags should delete the entry from Redis
	keys := tc.InvalidateAllTags(ctx, templateID)
	require.NotEmpty(t, keys, "should have deleted at least one key")

	// Verify Redis entry is gone
	exists, err = redisClient.Exists(ctx, redisKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "Redis entry should be deleted after InvalidateAllTags")
}

// TestTemplateCache_InvalidateAllTagsDoesNotInvalidateAliases tests that
// TemplateCache.InvalidateAllTags does NOT invalidate alias cache entries.
// Alias entries are only invalidated by InvalidateAlias (alias CRUD).
func TestTemplateCache_InvalidateAllTagsDoesNotInvalidateAliases(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "alias-all-tags", &teamSlug)

	cache := NewTemplateCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Resolve alias to populate alias cache
	_, err := cache.ResolveAlias(ctx, "alias-all-tags", teamSlug)
	require.NoError(t, err)

	// Verify alias key exists in Redis before invalidation
	aliasKey := cache.aliasCache.cache.RedisKey(buildAliasKey(&teamSlug, "alias-all-tags"))
	exists, err := redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "alias key should exist before invalidation")

	// Invalidate all tags — should NOT invalidate alias cache (alias→templateID mapping is stable)
	cache.InvalidateAllTags(ctx, templateID)

	// Alias key should still be present (alias→templateID doesn't change)
	exists, err = redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "alias key should survive InvalidateAllTags")
}

// TestTemplateCache_GetByID tests that GetByID returns correct template info
func TestTemplateCache_GetByID(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	cache := NewTemplateCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	info, err := cache.GetByID(ctx, templateID, nil)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, templateID, info.Template.TemplateID)
	assert.Equal(t, teamID, info.TeamID)
}

// TestTemplateCache_GetByID_NotFound tests that GetByID returns error for non-existent templates
func TestTemplateCache_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	cache := NewTemplateCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	info, err := cache.GetByID(ctx, "non-existent-id", nil)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)
}

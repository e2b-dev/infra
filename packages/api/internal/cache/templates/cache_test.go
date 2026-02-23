package templatecache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// TestAliasCacheResolve_BareAliasInTeamNamespace tests that a bare alias
// is found when it exists in the team's namespace
func TestAliasCacheResolve_BareAliasInTeamNamespace(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "my-alias", &teamSlug)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Bare alias should resolve via team namespace fallback
	info, err := cache.Resolve(ctx, "my-alias", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, templateID, info.TemplateID)
	assert.Equal(t, teamID, info.TeamID)
}

// TestAliasCacheResolve_BareAliasFallbackToNullNamespace tests that a bare alias
// falls back to NULL namespace (promoted templates) when not found in team namespace
func TestAliasCacheResolve_BareAliasFallbackToNullNamespace(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	// Create requesting team (has no aliases)
	requestingTeamID := testutils.CreateTestTeam(t, db)
	requestingTeamSlug := testutils.GetTeamSlug(t, ctx, db, requestingTeamID)

	// Create promoted template owned by another team
	promotedTeamID := testutils.CreateTestTeam(t, db)
	promotedTemplateID := testutils.CreateTestTemplate(t, db, promotedTeamID)

	// Create alias with NULL namespace (promoted)
	testutils.CreateTestTemplateAliasWithName(t, db, promotedTemplateID, "base", nil)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Bare alias "base" should:
	// 1. Try requesting team's namespace -> not found
	// 2. Fall back to NULL namespace -> found
	info, err := cache.Resolve(ctx, "base", requestingTeamSlug)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, promotedTemplateID, info.TemplateID)
	assert.Equal(t, promotedTeamID, info.TeamID)
}

// TestAliasCacheResolve_ExplicitNamespaceNoFallback tests that an explicit namespace
// does NOT fall back to NULL namespace
func TestAliasCacheResolve_ExplicitNamespaceNoFallback(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	// Create team
	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	// Create alias only in NULL namespace
	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "only-promoted", nil)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Explicit namespace lookup should NOT fall back to NULL
	info, err := cache.Resolve(ctx, teamSlug+"/only-promoted", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)
}

// TestAliasCacheResolve_ExplicitNamespaceNoIDFallback tests that an explicit namespace
// does NOT fall back to template ID lookup, even if the alias token matches a real template ID.
// This prevents "team-x/<templateID>" from resolving when no alias exists.
func TestAliasCacheResolve_ExplicitNamespaceNoIDFallback(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	// Create team and template
	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Request "team-slug/<templateID>" - no alias exists with this name in team namespace.
	// Even though templateID is a valid template ID, it should NOT resolve because
	// explicit namespace lookups should not fall back to ID lookup.
	info, err := cache.Resolve(ctx, teamSlug+"/"+templateID, teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)

	// But bare templateID should still resolve via ID fallback
	info, err = cache.Resolve(ctx, templateID, teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, templateID, info.TemplateID)
}

// TestAliasCacheResolve_TeamOverridesPromoted tests that team's alias
// takes precedence over promoted template with same name.
// NOTE: This test is for Phase 2 when PK becomes (alias, namespace).
// During Phase 1, PK is still (alias) only, so duplicate alias names are not allowed.
func TestAliasCacheResolve_TeamOverridesPromoted(t *testing.T) {
	t.Skip("Phase 2 test: requires PK change to (alias, namespace) to allow duplicate alias names")

	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	// Create team and its template
	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	teamTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	// Create promoted template
	promotedTeamID := testutils.CreateTestTeam(t, db)
	promotedTemplateID := testutils.CreateTestTemplate(t, db, promotedTeamID)

	// Both have alias "shared-name"
	testutils.CreateTestTemplateAliasWithName(t, db, promotedTemplateID, "shared-name", nil)
	testutils.CreateTestTemplateAliasWithName(t, db, teamTemplateID, "shared-name", &teamSlug)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Team's alias should take precedence
	info, err := cache.Resolve(ctx, "shared-name", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, teamTemplateID, info.TemplateID, "Team's alias should override promoted")
}

// TestAliasCacheResolve_DirectTemplateID tests that direct template IDs work
func TestAliasCacheResolve_DirectTemplateID(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Direct template ID should resolve
	info, err := cache.Resolve(ctx, templateID, teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, templateID, info.TemplateID)
}

// TestAliasCacheResolve_NotFound tests that non-existent aliases return 404
func TestAliasCacheResolve_NotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	info, err := cache.Resolve(ctx, "non-existent", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)
}

// TestAliasCacheLookupByID tests direct template ID lookup
func TestAliasCacheLookupByID(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	info, err := cache.LookupByID(ctx, templateID)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, templateID, info.TemplateID)
	assert.Equal(t, teamID, info.TeamID)
}

// TestAliasCacheLookupByID_NotFound tests that non-existent template IDs return 404
func TestAliasCacheLookupByID_NotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	info, err := cache.LookupByID(ctx, "non-existent-id")
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)
}

// TestAliasCacheLookupByID_UsesCache tests that LookupByID uses cached entries from Resolve
func TestAliasCacheLookupByID_UsesCache(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "cached-alias", &teamSlug)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// First, resolve by alias (this caches by both alias and template ID)
	_, err := cache.Resolve(ctx, "cached-alias", teamSlug)
	require.NoError(t, err)

	// Verify the template ID key was backfilled into Redis by the alias resolve
	idKey := cache.cache.RedisKey(templateID)
	exists, err := redis.Exists(ctx, idKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "template ID key should be cached after alias resolve")

	// Lookup by ID should return correct data from cache
	info2, err := cache.LookupByID(ctx, templateID)
	require.NoError(t, err)
	require.NotNil(t, info2)
	assert.Equal(t, templateID, info2.TemplateID)
	assert.Equal(t, teamID, info2.TeamID)
}

// TestAliasCacheResolve_NegativeCaching tests that not-found results are cached (tombstones)
func TestAliasCacheResolve_NegativeCaching(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// First lookup - not found, should cache tombstone
	info, err := cache.Resolve(ctx, "non-existent-alias", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)

	// Create the template and alias AFTER the first lookup
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "non-existent-alias", &teamSlug)

	// Second lookup should still return not found (cached tombstone)
	info, err = cache.Resolve(ctx, "non-existent-alias", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)
}

// TestAliasCacheResolve_NegativeCachingFallback tests that tombstones are cached
// for intermediate lookups during fallback resolution
func TestAliasCacheResolve_NegativeCachingFallback(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	// Create requesting team
	requestingTeamID := testutils.CreateTestTeam(t, db)
	requestingTeamSlug := testutils.GetTeamSlug(t, ctx, db, requestingTeamID)

	// Create promoted template with NULL namespace alias
	promotedTeamID := testutils.CreateTestTeam(t, db)
	promotedTemplateID := testutils.CreateTestTemplate(t, db, promotedTeamID)
	testutils.CreateTestTemplateAliasWithName(t, db, promotedTemplateID, "promoted-alias", nil)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Resolve bare alias - tries team namespace first (not found, caches tombstone),
	// then falls back to NULL namespace (found)
	info, err := cache.Resolve(ctx, "promoted-alias", requestingTeamSlug)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, promotedTemplateID, info.TemplateID)

	// Verify tombstone was cached for team namespace lookup
	tombstoneKey := cache.cache.RedisKey(buildAliasKey(&requestingTeamSlug, "promoted-alias"))
	exists, err := redis.Exists(ctx, tombstoneKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "tombstone should be cached for team namespace miss")

	// Verify positive result was cached for NULL namespace lookup
	positiveKey := cache.cache.RedisKey(buildAliasKey(nil, "promoted-alias"))
	exists, err = redis.Exists(ctx, positiveKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "positive result should be cached for NULL namespace hit")
}

// TestAliasCache_InvalidateByTemplateID tests that InvalidateByTemplateID
// removes all cache entries pointing to that template
func TestAliasCache_InvalidateByTemplateID(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "alias-to-invalidate", &teamSlug)

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	// Resolve to populate cache
	info1, err := cache.Resolve(ctx, "alias-to-invalidate", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info1)

	// Also lookup by ID to cache that entry
	_, err = cache.LookupByID(ctx, templateID)
	require.NoError(t, err)

	// Verify alias key exists in Redis before invalidation
	aliasKey := cache.cache.RedisKey(buildAliasKey(&teamSlug, "alias-to-invalidate"))
	exists, err := redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "alias key should exist before invalidation")

	// Invalidate by template ID
	cache.InvalidateByTemplateID(ctx, templateID)

	// Verify alias key is gone from Redis
	exists, err = redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "alias key should be deleted after invalidation")

	// Verify template ID key is gone from Redis
	idKey := cache.cache.RedisKey(templateID)
	exists, err = redis.Exists(ctx, idKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "template ID key should be deleted after invalidation")

	// Re-resolve should still work (fresh fetch from DB)
	info3, err := cache.Resolve(ctx, "alias-to-invalidate", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info3)
	assert.Equal(t, templateID, info3.TemplateID)
}

// TestTemplateCache_InvalidateDoesNotInvalidateAliases tests that TemplateCache.Invalidate
// does NOT invalidate alias cache entries (only InvalidateAllTags does)
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
	info1, err := cache.ResolveAlias(ctx, "alias-for-template", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info1)

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
	val, err := redisClient.Get(ctx, redisKey).Result()
	require.NoError(t, err)
	require.NotEmpty(t, val)

	// InvalidateAllTags should delete the entry from Redis
	keys := tc.InvalidateAllTags(ctx, templateID)
	require.NotEmpty(t, keys, "should have deleted at least one key")

	// Verify Redis entry is gone
	exists, err := redisClient.Exists(ctx, redisKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "Redis entry should be deleted after InvalidateAllTags")
}

// TestTemplateCache_InvalidateAllTagsAlsoInvalidatesAliases tests that
// TemplateCache.InvalidateAllTags also invalidates the alias cache entries
func TestTemplateCache_InvalidateAllTagsAlsoInvalidatesAliases(t *testing.T) {
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

	// Invalidate all tags (should also invalidate alias cache)
	cache.InvalidateAllTags(ctx, templateID)

	// Alias key should be gone from Redis
	exists, err = redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "alias key should be deleted after InvalidateAllTags")
}

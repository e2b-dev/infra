package templatecache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
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
	resolvedID, err := cache.Resolve(ctx, "my-alias", teamSlug)
	require.NoError(t, err)
	assert.Equal(t, templateID, resolvedID)
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
	resolvedID, err := cache.Resolve(ctx, "base", requestingTeamSlug)
	require.NoError(t, err)
	assert.Equal(t, promotedTemplateID, resolvedID)
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
	resolvedID, err := cache.Resolve(ctx, teamSlug+"/only-promoted", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.Empty(t, resolvedID)
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
	resolvedID, err := cache.Resolve(ctx, teamSlug+"/"+templateID, teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.Empty(t, resolvedID)

	// But bare templateID should still resolve via ID fallback
	resolvedID, err = cache.Resolve(ctx, templateID, teamSlug)
	require.NoError(t, err)
	assert.Equal(t, templateID, resolvedID)
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
	resolvedID, err := cache.Resolve(ctx, "shared-name", teamSlug)
	require.NoError(t, err)
	assert.Equal(t, teamTemplateID, resolvedID, "Team's alias should override promoted")
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
	resolvedID, err := cache.Resolve(ctx, templateID, teamSlug)
	require.NoError(t, err)
	assert.Equal(t, templateID, resolvedID)
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

	resolvedID, err := cache.Resolve(ctx, "non-existent", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.Empty(t, resolvedID)
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

	resolvedID, err := cache.LookupByID(ctx, templateID)
	require.NoError(t, err)
	assert.Equal(t, templateID, resolvedID)
}

// TestAliasCacheLookupByID_NotFound tests that non-existent template IDs return 404
func TestAliasCacheLookupByID_NotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	cache := NewAliasCache(db.SqlcClient, redis)
	defer cache.Close(ctx)

	resolvedID, err := cache.LookupByID(ctx, "non-existent-id")
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.Empty(t, resolvedID)
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
	resolvedID, err := cache.Resolve(ctx, "non-existent-alias", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.Empty(t, resolvedID)

	// Create the template and alias AFTER the first lookup
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "non-existent-alias", &teamSlug)

	// Second lookup should still return not found (cached tombstone)
	resolvedID, err = cache.Resolve(ctx, "non-existent-alias", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.Empty(t, resolvedID)
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
	resolvedID, err := cache.Resolve(ctx, "promoted-alias", requestingTeamSlug)
	require.NoError(t, err)
	assert.Equal(t, promotedTemplateID, resolvedID)

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

// TestAliasCacheResolve_InvalidateRefreshesCache tests that invalidating an alias
// entry allows fresh data to be fetched from DB on next lookup
func TestAliasCacheResolve_InvalidateRefreshesCache(t *testing.T) {
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
	resolvedID, err := cache.Resolve(ctx, "alias-to-invalidate", teamSlug)
	require.NoError(t, err)
	assert.Equal(t, templateID, resolvedID)

	// Verify alias key exists in Redis before invalidation
	aliasKey := cache.cache.RedisKey(buildAliasKey(&teamSlug, "alias-to-invalidate"))
	exists, err := redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "alias key should exist before invalidation")

	// Invalidate the specific alias
	cache.Invalidate(ctx, &teamSlug, "alias-to-invalidate")

	// Verify alias key is deleted after invalidation
	exists, err = redis.Exists(ctx, aliasKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "alias key should be deleted after invalidation")

	// Re-resolve should still work (fresh fetch from DB)
	resolvedID, err = cache.Resolve(ctx, "alias-to-invalidate", teamSlug)
	require.NoError(t, err)
	assert.Equal(t, templateID, resolvedID)
}

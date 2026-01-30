package templatecache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// TestAliasCacheResolve_BareAliasInTeamNamespace tests that a bare alias
// is found when it exists in the team's namespace
func TestAliasCacheResolve_BareAliasInTeamNamespace(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "my-alias", &teamSlug)

	cache := NewAliasCache(db.SqlcClient)
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
	ctx := t.Context()

	// Create requesting team (has no aliases)
	requestingTeamID := testutils.CreateTestTeam(t, db)
	requestingTeamSlug := testutils.GetTeamSlug(t, ctx, db, requestingTeamID)

	// Create promoted template owned by another team
	promotedTeamID := testutils.CreateTestTeam(t, db)
	promotedTemplateID := testutils.CreateTestTemplate(t, db, promotedTeamID)

	// Create alias with NULL namespace (promoted)
	testutils.CreateTestTemplateAliasWithName(t, db, promotedTemplateID, "base", nil)

	cache := NewAliasCache(db.SqlcClient)
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
	ctx := t.Context()

	// Create team
	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	// Create alias only in NULL namespace
	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "only-promoted", nil)

	cache := NewAliasCache(db.SqlcClient)
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
	ctx := t.Context()

	// Create team and template
	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	cache := NewAliasCache(db.SqlcClient)
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

	cache := NewAliasCache(db.SqlcClient)
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
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	cache := NewAliasCache(db.SqlcClient)
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
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)

	cache := NewAliasCache(db.SqlcClient)
	defer cache.Close(ctx)

	info, err := cache.Resolve(ctx, "non-existent", teamSlug)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)
}

// TestAliasCacheLookupByID tests direct template ID lookup
func TestAliasCacheLookupByID(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	cache := NewAliasCache(db.SqlcClient)
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
	ctx := t.Context()

	cache := NewAliasCache(db.SqlcClient)
	defer cache.Close(ctx)

	info, err := cache.LookupByID(ctx, "non-existent-id")
	require.ErrorIs(t, err, ErrTemplateNotFound)
	require.Nil(t, info)
}

// TestAliasCacheLookupByID_UsesCache tests that LookupByID uses cached entries from Resolve
func TestAliasCacheLookupByID_UsesCache(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "cached-alias", &teamSlug)

	cache := NewAliasCache(db.SqlcClient)
	defer cache.Close(ctx)

	// First, resolve by alias (this caches by both alias and template ID)
	info1, err := cache.Resolve(ctx, "cached-alias", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info1)

	// Lookup by ID should return the same cached pointer
	info2, err := cache.LookupByID(ctx, templateID)
	require.NoError(t, err)
	require.NotNil(t, info2)
	assert.Equal(t, templateID, info2.TemplateID)
	assert.Equal(t, teamID, info2.TeamID)

	// Same pointer proves cache was hit (not a fresh DB fetch)
	assert.Same(t, info1, info2)
}

// TestAliasCacheResolve_NegativeCaching tests that not-found results are cached (tombstones)
func TestAliasCacheResolve_NegativeCaching(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)

	cache := NewAliasCache(db.SqlcClient)
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
	ctx := t.Context()

	// Create requesting team
	requestingTeamID := testutils.CreateTestTeam(t, db)
	requestingTeamSlug := testutils.GetTeamSlug(t, ctx, db, requestingTeamID)

	// Create promoted template with NULL namespace alias
	promotedTeamID := testutils.CreateTestTeam(t, db)
	promotedTemplateID := testutils.CreateTestTemplate(t, db, promotedTeamID)
	testutils.CreateTestTemplateAliasWithName(t, db, promotedTemplateID, "promoted-alias", nil)

	cache := NewAliasCache(db.SqlcClient)
	defer cache.Close(ctx)

	// Resolve bare alias - tries team namespace first (not found, caches tombstone),
	// then falls back to NULL namespace (found)
	info1, err := cache.Resolve(ctx, "promoted-alias", requestingTeamSlug)
	require.NoError(t, err)
	require.NotNil(t, info1)
	assert.Equal(t, promotedTemplateID, info1.TemplateID)

	// Second resolve should use cached tombstone for team namespace lookup
	// and cached result for NULL namespace lookup (same pointer)
	info2, err := cache.Resolve(ctx, "promoted-alias", requestingTeamSlug)
	require.NoError(t, err)
	require.NotNil(t, info2)
	assert.Same(t, info1, info2)
}

// TestAliasCache_InvalidateByTemplateID tests that InvalidateByTemplateID
// removes all cache entries pointing to that template
func TestAliasCache_InvalidateByTemplateID(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "alias-to-invalidate", &teamSlug)

	cache := NewAliasCache(db.SqlcClient)
	defer cache.Close(ctx)

	// Resolve to populate cache
	info1, err := cache.Resolve(ctx, "alias-to-invalidate", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info1)

	// Also lookup by ID to cache that entry
	info2, err := cache.LookupByID(ctx, templateID)
	require.NoError(t, err)
	assert.Same(t, info1, info2)

	// Invalidate by template ID
	cache.InvalidateByTemplateID(templateID)

	// Next resolve should return a different pointer (fresh fetch)
	info3, err := cache.Resolve(ctx, "alias-to-invalidate", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info3)
	assert.NotSame(t, info1, info3)

	// LookupByID should also return a different pointer
	info4, err := cache.LookupByID(ctx, templateID)
	require.NoError(t, err)
	require.NotNil(t, info4)
	assert.NotSame(t, info1, info4)
}

// TestTemplateCache_InvalidateDoesNotInvalidateAliases tests that TemplateCache.Invalidate
// does NOT invalidate alias cache entries (only InvalidateAllTags does)
func TestTemplateCache_InvalidateDoesNotInvalidateAliases(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "alias-for-template", &teamSlug)

	cache := NewTemplateCache(db.SqlcClient)
	defer cache.Close(ctx)

	// Resolve alias to populate alias cache
	info1, err := cache.ResolveAlias(ctx, "alias-for-template", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info1)

	// Invalidate the template (should NOT invalidate alias cache)
	cache.Invalidate(templateID, nil)

	// Next resolve should return the same cached pointer
	info2, err := cache.ResolveAlias(ctx, "alias-for-template", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info2)
	assert.Same(t, info1, info2)
}

// TestTemplateCache_InvalidateAllTagsAlsoInvalidatesAliases tests that
// TemplateCache.InvalidateAllTags also invalidates the alias cache entries
func TestTemplateCache_InvalidateAllTagsAlsoInvalidatesAliases(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "alias-all-tags", &teamSlug)

	cache := NewTemplateCache(db.SqlcClient)
	defer cache.Close(ctx)

	// Resolve alias to populate alias cache
	info1, err := cache.ResolveAlias(ctx, "alias-all-tags", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info1)

	// Invalidate all tags (should also invalidate alias cache)
	cache.InvalidateAllTags(templateID)

	// Next resolve should return a different pointer (fresh fetch)
	info2, err := cache.ResolveAlias(ctx, "alias-all-tags", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, info2)
	assert.NotSame(t, info1, info2)
}

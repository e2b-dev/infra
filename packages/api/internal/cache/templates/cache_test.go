package templatecache

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

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

// TestTemplateCache_InvalidateAllTagsAlsoInvalidatesMetadata tests that
// InvalidateAllTags also invalidates the metadata cache
func TestTemplateCache_InvalidateAllTagsAlsoInvalidatesMetadata(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	tc := NewTemplateCache(db.SqlcClient, redis)
	defer tc.Close(ctx)

	// Populate metadata cache
	_, err := tc.metadataCache.Get(ctx, templateID)
	require.NoError(t, err)

	// Verify metadata key exists in Redis
	metadataKey := tc.metadataCache.cache.RedisKey(templateID)
	exists, err := redis.Exists(ctx, metadataKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "metadata key should exist before invalidation")

	// InvalidateAllTags should also invalidate metadata
	tc.InvalidateAllTags(ctx, templateID)

	// Metadata key should be gone
	exists, err = redis.Exists(ctx, metadataKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "metadata key should be deleted after InvalidateAllTags")
}

func TestTemplateCache_Get_TagNotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	tc := NewTemplateCache(db.SqlcClient, redis)
	defer tc.Close(ctx)

	missingTag := "v-does-not-exist"
	_, _, err := tc.Get(ctx, templateID, &missingTag, teamID, consts.LocalClusterID)
	require.ErrorIs(t, err, ErrTemplateTagNotFound)
	assert.NotErrorIs(t, err, ErrTemplateNotFound)
}

func TestRefineTemplateNotFound(t *testing.T) {
	t.Parallel()

	templateID := "tmpl-1"
	ownerTeamID := uuid.New()
	otherTeamID := uuid.New()
	origErr := ErrTemplateNotFound

	ownerLookup := func(context.Context, string) (*AliasInfo, error) { //nolint:unparam // stub signature fixed by callback type
		return &AliasInfo{TemplateID: templateID, TeamID: ownerTeamID}, nil
	}

	t.Run("owner of private template gets tag-not-found", func(t *testing.T) {
		t.Parallel()
		_, span := tracer.Start(t.Context(), "t")
		defer span.End()

		err := refineTemplateNotFound(
			t.Context(), span, origErr, templateID, ownerTeamID,
			ownerLookup,
			func(context.Context, string) (*TemplateMetadata, error) {
				return &TemplateMetadata{TemplateID: templateID, TeamID: ownerTeamID, Public: false}, nil
			},
		)
		assert.ErrorIs(t, err, ErrTemplateTagNotFound)
	})

	t.Run("foreign team on public template gets tag-not-found", func(t *testing.T) {
		t.Parallel()
		_, span := tracer.Start(t.Context(), "t")
		defer span.End()

		err := refineTemplateNotFound(
			t.Context(), span, origErr, templateID, otherTeamID,
			ownerLookup,
			func(context.Context, string) (*TemplateMetadata, error) {
				return &TemplateMetadata{TemplateID: templateID, TeamID: ownerTeamID, Public: true}, nil
			},
		)
		assert.ErrorIs(t, err, ErrTemplateTagNotFound)
	})

	t.Run("foreign team on private template keeps original error", func(t *testing.T) {
		t.Parallel()
		_, span := tracer.Start(t.Context(), "t")
		defer span.End()

		err := refineTemplateNotFound(
			t.Context(), span, origErr, templateID, otherTeamID,
			ownerLookup,
			func(context.Context, string) (*TemplateMetadata, error) {
				return &TemplateMetadata{TemplateID: templateID, TeamID: ownerTeamID, Public: false}, nil
			},
		)
		require.ErrorIs(t, err, ErrTemplateNotFound)
		assert.NotErrorIs(t, err, ErrTemplateTagNotFound)
	})

	t.Run("template genuinely missing keeps original error", func(t *testing.T) {
		t.Parallel()
		_, span := tracer.Start(t.Context(), "t")
		defer span.End()

		getMetadataShouldNotRun := func(context.Context, string) (*TemplateMetadata, error) {
			t.Fatalf("metadata lookup must not run when template is missing")

			return nil, nil
		}

		err := refineTemplateNotFound(
			t.Context(), span, origErr, templateID, ownerTeamID,
			func(context.Context, string) (*AliasInfo, error) {
				return nil, ErrTemplateNotFound
			},
			getMetadataShouldNotRun,
		)
		require.ErrorIs(t, err, ErrTemplateNotFound)
		assert.NotErrorIs(t, err, ErrTemplateTagNotFound)
	})

	t.Run("transient lookup error falls back to original error", func(t *testing.T) {
		t.Parallel()
		_, span := tracer.Start(t.Context(), "t")
		defer span.End()

		getMetadataShouldNotRun := func(context.Context, string) (*TemplateMetadata, error) {
			t.Fatalf("metadata lookup must not run on transient existence failure")

			return nil, nil
		}

		err := refineTemplateNotFound(
			t.Context(), span, origErr, templateID, ownerTeamID,
			func(context.Context, string) (*AliasInfo, error) {
				return nil, errors.New("redis: connection refused")
			},
			getMetadataShouldNotRun,
		)
		require.ErrorIs(t, err, ErrTemplateNotFound)
		assert.NotErrorIs(t, err, ErrTemplateTagNotFound)
	})

	t.Run("transient metadata error falls back to original error", func(t *testing.T) {
		t.Parallel()
		_, span := tracer.Start(t.Context(), "t")
		defer span.End()

		err := refineTemplateNotFound(
			t.Context(), span, origErr, templateID, ownerTeamID,
			ownerLookup,
			func(context.Context, string) (*TemplateMetadata, error) {
				return nil, errors.New("redis: i/o timeout")
			},
		)
		require.ErrorIs(t, err, ErrTemplateNotFound)
		assert.NotErrorIs(t, err, ErrTemplateTagNotFound)
	})
}

func TestTemplateCache_Get_TagNotFound_HidesExistenceFromForeignTeam(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	templateID := "base-env-" + uuid.New().String()
	require.NoError(t, db.SqlcClient.TestsRawSQL(ctx,
		"INSERT INTO public.envs (id, team_id, public, updated_at, source) VALUES ($1, $2, false, NOW(), 'template')",
		templateID, ownerTeamID,
	))
	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "default")

	foreignTeamID := testutils.CreateTestTeam(t, db)

	tc := NewTemplateCache(db.SqlcClient, redis)
	defer tc.Close(ctx)

	missingTag := "bogus"
	_, _, err := tc.Get(ctx, templateID, &missingTag, foreignTeamID, consts.LocalClusterID)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.NotErrorIs(t, err, ErrTemplateTagNotFound)
}

func TestTemplateCache_Get_TemplateNotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	tc := NewTemplateCache(db.SqlcClient, redis)
	defer tc.Close(ctx)

	teamID := uuid.New()
	missingTemplate := "nonexistent-template-" + uuid.New().String()
	tag := "any-tag"
	_, _, err := tc.Get(ctx, missingTemplate, &tag, teamID, consts.LocalClusterID)
	require.ErrorIs(t, err, ErrTemplateNotFound)
	assert.NotErrorIs(t, err, ErrTemplateTagNotFound)
}

// TestTemplateCache_ResolveAliasWithMetadata tests the combined resolution
func TestTemplateCache_ResolveAliasWithMetadata(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)

	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "meta-alias", &teamSlug)

	tc := NewTemplateCache(db.SqlcClient, redis)
	defer tc.Close(ctx)

	aliasInfo, metadata, err := tc.ResolveAliasWithMetadata(ctx, "meta-alias", teamSlug)
	require.NoError(t, err)
	require.NotNil(t, aliasInfo)
	require.NotNil(t, metadata)

	assert.Equal(t, templateID, aliasInfo.TemplateID)
	assert.Equal(t, teamID, aliasInfo.TeamID)
	assert.Equal(t, templateID, metadata.TemplateID)
	assert.Equal(t, teamID, metadata.TeamID)
	assert.Equal(t, consts.LocalClusterID, metadata.ClusterID)
}

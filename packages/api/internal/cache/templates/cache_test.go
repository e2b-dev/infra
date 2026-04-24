package templatecache

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
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

func TestTemplateCache_Get_MissingBuildReturnsTemplateNotFound(t *testing.T) {
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

func TestTemplateCache_TranslateGetError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		requesterTeam string
		public        bool
		wantErr       error
	}{
		{
			name:          "owner gets tag not found",
			requesterTeam: "owner",
			public:        false,
			wantErr:       ErrTemplateTagNotFound,
		},
		{
			name:          "foreign team gets tag not found for public template",
			requesterTeam: "foreign",
			public:        true,
			wantErr:       ErrTemplateTagNotFound,
		},
		{
			name:          "foreign team keeps generic not found for private template",
			requesterTeam: "foreign",
			public:        false,
			wantErr:       ErrTemplateNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testutils.SetupDatabase(t)
			redis := redis_utils.SetupInstance(t)
			ctx := t.Context()

			ownerTeamID := testutils.CreateTestTeam(t, db)
			ownerSlug := testutils.GetTeamSlug(t, ctx, db, ownerTeamID)
			requesterTeamID := ownerTeamID
			requesterSlug := ownerSlug

			if tt.requesterTeam == "foreign" {
				requesterTeamID = testutils.CreateTestTeam(t, db)
				requesterSlug = testutils.GetTeamSlug(t, ctx, db, requesterTeamID)
			}

			templateID := testutils.CreateTestTemplate(t, db, ownerTeamID)
			setTemplatePublic(t, db, templateID, tt.public)
			testutils.CreateTestTemplateAliasWithName(t, db, templateID, "translate-miss", &ownerSlug)

			buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
			testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, id.DefaultTag)

			tc := NewTemplateCache(db.SqlcClient, redis)
			defer tc.Close(ctx)

			identifier := id.WithNamespace(ownerSlug, "translate-miss")
			aliasInfo, err := tc.ResolveAlias(ctx, identifier, requesterSlug)
			require.NoError(t, err)

			missingTag := "v2"
			_, _, err = tc.Get(ctx, aliasInfo.TemplateID, &missingTag, requesterTeamID, consts.LocalClusterID)
			require.ErrorIs(t, err, ErrTemplateNotFound)

			err = tc.TranslateGetError(ctx, err, aliasInfo, requesterTeamID)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
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

func setTemplatePublic(t *testing.T, db *testutils.Database, templateID string, public bool) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(
		t.Context(),
		"UPDATE public.envs SET public = $2 WHERE id = $1",
		templateID,
		public,
	)
	require.NoError(t, err)
}

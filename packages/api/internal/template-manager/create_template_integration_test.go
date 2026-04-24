package template_manager

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestSetTemplateSource_FromTemplateUsesResolvedBuild(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "base-template", &teamSlug)

	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, "v1")

	cache := templatecache.NewTemplateCache(db.SqlcClient, redis)
	defer func() {
		require.NoError(t, cache.Close(ctx))
	}()

	tm := &TemplateManager{
		sqlcDB:        db.SqlcClient,
		templateCache: cache,
	}

	fromTemplate := id.WithTag(id.WithNamespace(teamSlug, "base-template"), "v1")
	template := &templatemanagergrpc.TemplateConfig{}

	err := setTemplateSource(ctx, tm, teamID, teamSlug, template, nil, &fromTemplate)
	require.NoError(t, err)

	require.NotNil(t, template.GetFromTemplate())
	assert.Equal(t, fromTemplate, template.GetFromTemplate().Alias)
	assert.Equal(t, buildID.String(), template.GetFromTemplate().BuildID)
}

func TestSetTemplateSource_FromTemplateMissingBuildReturnsNotFound(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	teamSlug := testutils.GetTeamSlug(t, ctx, db, teamID)
	templateID := testutils.CreateTestTemplate(t, db, teamID)
	testutils.CreateTestTemplateAliasWithName(t, db, templateID, "base-template-missing", &teamSlug)

	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, id.DefaultTag)

	cache := templatecache.NewTemplateCache(db.SqlcClient, redis)
	defer func() {
		require.NoError(t, cache.Close(ctx))
	}()

	tm := &TemplateManager{
		sqlcDB:        db.SqlcClient,
		templateCache: cache,
	}

	fromTemplate := id.WithTag(id.WithNamespace(teamSlug, "base-template-missing"), "v2")
	template := &templatemanagergrpc.TemplateConfig{}

	err := setTemplateSource(ctx, tm, teamID, teamSlug, template, nil, &fromTemplate)
	require.Error(t, err)

	var fromTemplateErr *FromTemplateError
	require.ErrorAs(t, err, &fromTemplateErr)
	assert.Equal(t, fmt.Sprintf("base template '%s' not found", fromTemplate), err.Error())
	assert.Nil(t, template.GetFromTemplate())
}

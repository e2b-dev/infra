package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestPostSandboxes_MissingTagDisclosure(t *testing.T) {
	t.Parallel()

	t.Run("public template returns tag-specific not found", func(t *testing.T) {
		t.Parallel()
		assertMissingTagDisclosure(t, true, "public-missing-tag")
	})

	t.Run("private template stays generic not found", func(t *testing.T) {
		t.Parallel()
		assertMissingTagDisclosure(t, false, "private-missing-tag")
	})
}

func assertMissingTagDisclosure(t *testing.T, public bool, alias string) {
	t.Helper()

	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	ownerTeamSlug := testutils.GetTeamSlug(t, ctx, db, ownerTeamID)
	requesterTeamID := testutils.CreateTestTeam(t, db)
	requesterTeamSlug := testutils.GetTeamSlug(t, ctx, db, requesterTeamID)

	store := &APIStore{
		templateCache: templatecache.NewTemplateCache(db.SqlcClient, redis),
	}
	defer func() {
		require.NoError(t, store.templateCache.Close(ctx))
	}()

	templateID := createTestTemplate(ctx, t, db, ownerTeamID)
	setTemplatePublic(ctx, t, db, templateID, public)
	createTestTemplateAliasWithName(ctx, t, db, templateID, alias, &ownerTeamSlug)

	buildID := testutils.CreateTestBuild(t, ctx, db, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, db, templateID, buildID, id.DefaultTag)

	templateRef := id.WithTag(id.WithNamespace(ownerTeamSlug, alias), "v2")
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	body, err := json.Marshal(api.PostSandboxesJSONRequestBody{TemplateID: templateRef})
	require.NoError(t, err)

	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/sandboxes", bytes.NewReader(body)).WithContext(ctx)
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{
			ID:   requesterTeamID,
			Slug: requesterTeamSlug,
		},
		Limits: &authtypes.TeamLimits{MaxLengthHours: 24},
	})

	//nolint:contextcheck // PostSandboxes reads ctx from ginCtx.Request.Context().
	store.PostSandboxes(ginCtx)

	require.Equal(t, http.StatusNotFound, recorder.Code)

	var apiErr api.Error
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &apiErr))

	wantMessage := fmt.Sprintf("template '%s' (%s) not found", id.WithNamespace(ownerTeamSlug, alias), templateID)
	if public {
		wantMessage = fmt.Sprintf("template '%s' (%s) with tag 'v2' not found", id.WithNamespace(ownerTeamSlug, alias), templateID)
	}

	assert.Equal(t, int32(http.StatusNotFound), apiErr.Code)
	assert.Equal(t, wantMessage, apiErr.Message)
}

func setTemplatePublic(ctx context.Context, t *testing.T, db *testutils.Database, templateID string, public bool) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(
		ctx,
		"UPDATE public.envs SET public = $2 WHERE id = $1",
		templateID,
		public,
	)
	require.NoError(t, err)
}

func createTestTemplate(ctx context.Context, t *testing.T, db *testutils.Database, teamID uuid.UUID) string {
	t.Helper()

	templateID := "base-env-" + uuid.New().String()

	err := db.SqlcClient.TestsRawSQL(
		ctx,
		"INSERT INTO public.envs (id, team_id, public, updated_at, source) VALUES ($1, $2, $3, NOW(), 'template')",
		templateID,
		teamID,
		true,
	)
	require.NoError(t, err)

	return templateID
}

func createTestTemplateAliasWithName(ctx context.Context, t *testing.T, db *testutils.Database, templateID, aliasName string, namespace *string) {
	t.Helper()

	err := db.SqlcClient.TestsRawSQL(
		ctx,
		"INSERT INTO public.env_aliases (alias, env_id, is_renamable, namespace) VALUES ($1, $2, $3, $4)",
		aliasName,
		templateID,
		true,
		namespace,
	)
	require.NoError(t, err)
}

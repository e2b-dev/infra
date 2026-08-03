package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apispec "github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	testqueries "github.com/e2b-dev/infra/packages/db/pkg/testutils/queries"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func newTemplateTagsStore(t *testing.T, testDB *testutils.Database) *APIStore {
	t.Helper()

	return &APIStore{
		sqlcDB:        testDB.SqlcClient,
		authDB:        testDB.AuthDb,
		templateCache: templatecache.NewTemplateCache(testDB.SqlcClient, redis_utils.SetupInstance(t)),
	}
}

func listTemplateTagsForTeam(
	t *testing.T,
	store *APIStore,
	teamID uuid.UUID,
	teamSlug string,
	templateRef string,
) *apispec.GetTemplatesTemplateIDTagsResponse {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("/templates/%s/tags", templateRef), nil)
	auth.SetTeamInfoForTest(t, c, &types.Team{
		Team: &authqueries.Team{
			ID:   teamID,
			Slug: teamSlug,
		},
	})

	store.GetTemplatesTemplateIDTags(c, templateRef)

	res, err := apispec.ParseGetTemplatesTemplateIDTagsResponse(w.Result())
	require.NoError(t, err)

	return res
}

// createPrivateTestTemplate inserts directly: testutils.CreateTestTemplate hardcodes public = true.
func createPrivateTestTemplate(t *testing.T, testDB *testutils.Database, teamID uuid.UUID) string {
	t.Helper()
	templateID := "base-env-" + uuid.New().String()

	err := testDB.TestQueries.InsertTestEnv(t.Context(), testqueries.InsertTestEnvParams{
		ID:     templateID,
		TeamID: teamID,
		Public: false,
		Source: "template",
	})
	require.NoError(t, err, "Failed to create private test template")

	return templateID
}

func TestGetTemplateTagsAsOwnerTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	teamSlug := testutils.GetTeamSlug(t, ctx, testDB, teamID)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "uploaded")
	testutils.CreateTestBuildAssignment(t, ctx, testDB, templateID, buildID, "dev")

	store := newTemplateTagsStore(t, testDB)

	res := listTemplateTagsForTeam(t, store, teamID, teamSlug, templateID)

	require.Equal(t, http.StatusOK, res.StatusCode(), string(res.Body))
	require.NotNil(t, res.JSON200)
	require.Len(t, *res.JSON200, 1)
	assert.Equal(t, "dev", (*res.JSON200)[0].Tag)
	assert.Equal(t, buildID, (*res.JSON200)[0].BuildID)
}

// EN-617: public templates are usable cross-team, so listing their tags must not be owner-only.
func TestGetTemplateTagsPublicTemplateAsForeignTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, testDB)
	foreignTeamID := testutils.CreateTestTeam(t, testDB)
	foreignTeamSlug := testutils.GetTeamSlug(t, ctx, testDB, foreignTeamID)

	// CreateTestTemplate seeds a public template.
	templateID := testutils.CreateTestTemplate(t, testDB, ownerTeamID)
	buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "uploaded")
	testutils.CreateTestBuildAssignment(t, ctx, testDB, templateID, buildID, "dev")

	store := newTemplateTagsStore(t, testDB)

	res := listTemplateTagsForTeam(t, store, foreignTeamID, foreignTeamSlug, templateID)

	require.Equal(t, http.StatusOK, res.StatusCode(), string(res.Body))
	require.NotNil(t, res.JSON200)
	require.Len(t, *res.JSON200, 1)
	assert.Equal(t, "dev", (*res.JSON200)[0].Tag)
	assert.Equal(t, buildID, (*res.JSON200)[0].BuildID)
}

func TestGetTemplateTagsPrivateTemplateAsForeignTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, testDB)
	foreignTeamID := testutils.CreateTestTeam(t, testDB)
	foreignTeamSlug := testutils.GetTeamSlug(t, ctx, testDB, foreignTeamID)

	templateID := createPrivateTestTemplate(t, testDB, ownerTeamID)
	buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "uploaded")
	testutils.CreateTestBuildAssignment(t, ctx, testDB, templateID, buildID, "dev")

	store := newTemplateTagsStore(t, testDB)

	res := listTemplateTagsForTeam(t, store, foreignTeamID, foreignTeamSlug, templateID)

	require.Equal(t, http.StatusForbidden, res.StatusCode(), string(res.Body))
}

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestGetTemplatesTemplateID_ReturnsTemplateForOwningTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)
	alias := testutils.CreateTestTemplateAlias(t, testDB, templateID)

	buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
	testutils.CreateTestBuildAssignment(t, ctx, testDB, templateID, buildID, "default")

	resp, status := callTemplateGetHandler(t, ctx, testDB, teamID, templateID)
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, templateID, resp.TemplateID)
	assert.Equal(t, buildID.String(), resp.BuildID)
	assert.True(t, resp.Public)
	assert.Contains(t, resp.Aliases, alias)
	assert.Contains(t, resp.Names, alias)
	require.NotNil(t, resp.CpuCount)
	assert.EqualValues(t, 2, *resp.CpuCount)
	require.NotNil(t, resp.MemoryMB)
	assert.EqualValues(t, 2048, *resp.MemoryMB)
}

func TestGetTemplatesTemplateID_ReturnsNotFoundWhenMissing(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)

	_, status := callTemplateGetHandler(t, ctx, testDB, teamID, "does-not-exist")
	assert.Equal(t, http.StatusNotFound, status)
}

func TestGetTemplatesTemplateID_IsolatesTemplatesAcrossTeams(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	ownerTeamID := testutils.CreateTestTeam(t, testDB)
	otherTeamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, ownerTeamID)

	_, status := callTemplateGetHandler(t, ctx, testDB, otherTeamID, templateID)
	assert.Equal(t, http.StatusNotFound, status,
		"a different team should see the template as not found, never get its contents")
}

func TestGetTemplatesTemplateID_ReturnsZeroBuildIDWhenNoReadyBuild(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	resp, status := callTemplateGetHandler(t, ctx, testDB, teamID, templateID)
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, uuid.Nil.String(), resp.BuildID)
	assert.Nil(t, resp.CpuCount)
	assert.Nil(t, resp.MemoryMB)
	assert.Nil(t, resp.DiskSizeMB)
	assert.Nil(t, resp.EnvdVersion)
}

func callTemplateGetHandler(t *testing.T, ctx context.Context, testDB *testutils.Database, teamID uuid.UUID, templateID string) (api.TemplateDetail, int) {
	t.Helper()

	recorder, ginCtx := newTemplateGetTestContext(t, ctx, teamID)
	store := &APIStore{db: testDB.SqlcClient}
	//nolint:contextcheck // GetTemplatesTemplateID reads ctx from ginCtx.Request.Context().
	store.GetTemplatesTemplateID(ginCtx, templateID)

	if recorder.Code != http.StatusOK {
		return api.TemplateDetail{}, recorder.Code
	}

	var response api.TemplateDetail
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	return response, recorder.Code
}

func newTemplateGetTestContext(t *testing.T, ctx context.Context, teamID uuid.UUID) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	return recorder, ginCtx
}

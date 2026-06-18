package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestGetTemplatesTemplateIDTagsGroups_ReturnsReadyAssignmentsLatestFirst(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)
	oldBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
	newBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	oldAssignmentID := insertTemplateTagAssignment(t, ctx, testDB, templateID, oldBuildID, "prod", baseTime)
	newAssignmentID := insertTemplateTagAssignment(t, ctx, testDB, templateID, newBuildID, "prod", baseTime.Add(time.Hour))

	response := callTagGroupsHandler(t, ctx, testDB, teamID, templateID, nil)

	require.Len(t, response.Tags, 1)
	group := response.Tags[0]
	assert.Equal(t, "prod", group.Tag)
	assert.False(t, group.HasMore)
	require.Len(t, group.Assignments, 2)
	assert.Equal(t, newAssignmentID, group.Assignments[0].AssignmentId)
	assert.Equal(t, newBuildID, group.Assignments[0].BuildId)
	assert.Equal(t, oldAssignmentID, group.Assignments[1].AssignmentId)
	assert.Equal(t, oldBuildID, group.Assignments[1].BuildId)
}

func TestGetTemplatesTemplateIDTagsGroups_LimitsPerTagAndSetsHasMore(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	for i := range 7 {
		buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
		insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, "prod", baseTime.Add(time.Duration(i)*time.Hour))
	}

	limit := api.TagAssignmentLimit(6)
	response := callTagGroupsHandler(t, ctx, testDB, teamID, templateID, &limit)

	require.Len(t, response.Tags, 1)
	assert.True(t, response.Tags[0].HasMore)
	assert.Len(t, response.Tags[0].Assignments, 6)
}

func TestGetTemplatesTemplateIDTagsGroups_DoesNotDedupeDuplicateBuildAssignments(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)
	buildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	firstAssignmentID := insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, "prod", baseTime)
	secondAssignmentID := insertTemplateTagAssignment(t, ctx, testDB, templateID, buildID, "prod", baseTime.Add(time.Hour))

	response := callTagGroupsHandler(t, ctx, testDB, teamID, templateID, nil)

	require.Len(t, response.Tags, 1)
	require.Len(t, response.Tags[0].Assignments, 2)
	assert.Equal(t, secondAssignmentID, response.Tags[0].Assignments[0].AssignmentId)
	assert.Equal(t, firstAssignmentID, response.Tags[0].Assignments[1].AssignmentId)
	assert.Equal(t, buildID, response.Tags[0].Assignments[0].BuildId)
	assert.Equal(t, buildID, response.Tags[0].Assignments[1].BuildId)
}

func TestGetTemplatesTemplateIDTagsGroups_OmitsTagsWithoutReadyAssignments(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)
	failedBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "failed")
	waitingBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "waiting")

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, failedBuildID, "prod", baseTime)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, waitingBuildID, "dev", baseTime.Add(time.Hour))

	response := callTagGroupsHandler(t, ctx, testDB, teamID, templateID, nil)

	assert.Empty(t, response.Tags)
}

func TestGetTemplatesTemplateIDTagsGroups_GroupsMultipleTagsByLatestAssignment(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)
	prodBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
	devBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, prodBuildID, "prod", baseTime)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, devBuildID, "dev", baseTime.Add(time.Hour))

	response := callTagGroupsHandler(t, ctx, testDB, teamID, templateID, nil)

	require.Len(t, response.Tags, 2)
	assert.Equal(t, "dev", response.Tags[0].Tag)
	assert.Equal(t, "prod", response.Tags[1].Tag)
}

func TestGetTemplatesTemplateIDTagsGroups_NotFoundForOtherTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	ownerTeamID := testutils.CreateTestTeam(t, testDB)
	otherTeamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, ownerTeamID)

	recorder, ginCtx := newTemplateTagsTestContext(t, ctx, otherTeamID)
	store := &APIStore{db: testDB.SqlcClient}

	store.GetTemplatesTemplateIDTagsGroups(ginCtx, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{})

	assert.Equal(t, http.StatusNotFound, recorder.Code,
		"team mismatch should not leak template existence")
}

func TestGetTemplatesTemplateIDTagsExists_ReturnsTrueOnlyForReadyTag(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)
	readyBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "ready")
	failedBuildID := testutils.CreateTestBuild(t, ctx, testDB, templateID, "failed")

	baseTime := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, readyBuildID, "prod", baseTime)
	insertTemplateTagAssignment(t, ctx, testDB, templateID, failedBuildID, "dev", baseTime)

	prodExists := callTagExistsHandler(t, ctx, testDB, teamID, templateID, "prod")
	assert.True(t, prodExists.Exists)
	assert.Equal(t, "prod", prodExists.NormalizedTag)

	normalizedProdExists := callTagExistsHandler(t, ctx, testDB, teamID, templateID, " PROD ")
	assert.True(t, normalizedProdExists.Exists)
	assert.Equal(t, "prod", normalizedProdExists.NormalizedTag)

	devExists := callTagExistsHandler(t, ctx, testDB, teamID, templateID, "dev")
	assert.False(t, devExists.Exists)
	assert.Equal(t, "dev", devExists.NormalizedTag)

	missingExists := callTagExistsHandler(t, ctx, testDB, teamID, templateID, "missing")
	assert.False(t, missingExists.Exists)
	assert.Equal(t, "missing", missingExists.NormalizedTag)
}

func TestGetTemplatesTemplateIDTagsExists_InvalidTagReturnsBadRequest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	teamID := testutils.CreateTestTeam(t, testDB)
	templateID := testutils.CreateTestTemplate(t, testDB, teamID)
	recorder, ginCtx := newTemplateTagsTestContext(t, ctx, teamID)
	store := &APIStore{db: testDB.SqlcClient}

	store.GetTemplatesTemplateIDTagsExists(ginCtx, templateID, api.GetTemplatesTemplateIDTagsExistsParams{Tag: "bad!tag"})

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}

func callTagGroupsHandler(t *testing.T, ctx context.Context, testDB *testutils.Database, teamID uuid.UUID, templateID string, assignmentLimit *api.TagAssignmentLimit) api.TemplateTagGroupsResponse {
	t.Helper()

	recorder, ginCtx := newTemplateTagsTestContext(t, ctx, teamID)
	store := &APIStore{db: testDB.SqlcClient}
	//nolint:contextcheck // GetTemplatesTemplateIDTagsGroups reads ctx from ginCtx.Request.Context().
	store.GetTemplatesTemplateIDTagsGroups(ginCtx, templateID, api.GetTemplatesTemplateIDTagsGroupsParams{
		AssignmentLimit: assignmentLimit,
	})

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response api.TemplateTagGroupsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	return response
}

func callTagExistsHandler(t *testing.T, ctx context.Context, testDB *testutils.Database, teamID uuid.UUID, templateID string, tag string) api.TemplateTagExistsResponse {
	t.Helper()

	recorder, ginCtx := newTemplateTagsTestContext(t, ctx, teamID)
	store := &APIStore{db: testDB.SqlcClient}
	//nolint:contextcheck // GetTemplatesTemplateIDTagsExists reads ctx from ginCtx.Request.Context().
	store.GetTemplatesTemplateIDTagsExists(ginCtx, templateID, api.GetTemplatesTemplateIDTagsExistsParams{Tag: tag})

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response api.TemplateTagExistsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	return response
}

func newTemplateTagsTestContext(t *testing.T, ctx context.Context, teamID uuid.UUID) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	return recorder, ginCtx
}

func insertTemplateTagAssignment(t *testing.T, ctx context.Context, testDB *testutils.Database, templateID string, buildID uuid.UUID, tag string, assignedAt time.Time) uuid.UUID {
	t.Helper()

	var assignmentID uuid.UUID
	err := testDB.SqlcClient.TestsRawSQLQuery(ctx,
		`INSERT INTO public.env_build_assignments (env_id, build_id, tag, source, created_at)
		VALUES ($1, $2, $3, 'app', $4)
		RETURNING id`,
		func(rows pgx.Rows) error {
			if rows.Next() {
				return rows.Scan(&assignmentID)
			}

			return pgx.ErrNoRows
		},
		templateID, buildID, tag, assignedAt,
	)
	require.NoError(t, err, "Failed to create build assignment")

	return assignmentID
}

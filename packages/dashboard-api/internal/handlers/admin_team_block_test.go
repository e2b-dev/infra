package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestPutAdminTeamsTeamIDBlockBlocksTeamWithReason(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	response := callBlockTeam(t, store, teamID, `{"reason":"  payment overdue "}`)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())

	blocked, reason := teamBlockState(t, db, teamID)
	require.True(t, blocked)
	require.Equal(t, "payment overdue", *reason)
	require.Equal(t, []uuid.UUID{teamID}, authService.invalidated)
}

func TestPutAdminTeamsTeamIDBlockReplacesTheReason(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	store := &APIStore{db: db.SqlcClient, authService: &recordingCacheAuthService{}}

	require.Equal(t, http.StatusNoContent, callBlockTeam(t, store, teamID, `{"reason":"first"}`).Code)
	require.Equal(t, http.StatusNoContent, callBlockTeam(t, store, teamID, `{"reason":"second"}`).Code)

	blocked, reason := teamBlockState(t, db, teamID)
	require.True(t, blocked)
	require.Equal(t, "second", *reason)
}

func TestPutAdminTeamsTeamIDBlockRejectsAMissingReason(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	for _, body := range []string{`{}`, `{"reason":"   "}`, `not json`} {
		response := callBlockTeam(t, store, teamID, body)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}

	blocked, _ := teamBlockState(t, db, teamID)
	require.False(t, blocked)
	require.Empty(t, authService.invalidated)
}

func TestDeleteAdminTeamsTeamIDBlockClearsBlockAndReason(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET is_blocked = true, blocked_reason = 'abuse' WHERE id = $1`, teamID))
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	response := callUnblockTeam(t, store, teamID)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())

	blocked, reason := teamBlockState(t, db, teamID)
	require.False(t, blocked)
	require.Nil(t, reason)
	require.Equal(t, []uuid.UUID{teamID}, authService.invalidated)
}

func TestSetTeamBlockReturnsNotFoundForUnknownTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	require.Equal(t, http.StatusNotFound, callBlockTeam(t, store, uuid.New(), `{"reason":"abuse"}`).Code)
	require.Equal(t, http.StatusNotFound, callUnblockTeam(t, store, uuid.New()).Code)
	require.Empty(t, authService.invalidated)
}

func callBlockTeam(t *testing.T, store *APIStore, teamID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/admin/teams/"+teamID.String()+"/block", strings.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	store.PutAdminTeamsTeamIDBlock(ginCtx, teamID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func callUnblockTeam(t *testing.T, store *APIStore, teamID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/admin/teams/"+teamID.String()+"/block", nil)
	store.DeleteAdminTeamsTeamIDBlock(ginCtx, teamID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func teamBlockState(t *testing.T, db *testutils.Database, teamID uuid.UUID) (bool, *string) {
	t.Helper()

	var (
		blocked bool
		reason  *string
	)
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(t.Context(),
		`SELECT is_blocked, blocked_reason FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&blocked, &reason)
		},
		teamID,
	))

	return blocked, reason
}

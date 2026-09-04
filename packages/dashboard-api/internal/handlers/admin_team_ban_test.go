package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestPutAdminTeamsTeamIDBanBansTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	response := callSetTeamBan(t, store, teamID, http.MethodPut)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	require.True(t, teamIsBanned(t, db, teamID))
	require.Equal(t, []uuid.UUID{teamID}, authService.invalidated)
}

func TestPutAdminTeamsTeamIDBanIsIdempotent(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET is_banned = true WHERE id = $1`, teamID))
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	response := callSetTeamBan(t, store, teamID, http.MethodPut)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	require.True(t, teamIsBanned(t, db, teamID))
	require.Equal(t, []uuid.UUID{teamID}, authService.invalidated)
}

func TestDeleteAdminTeamsTeamIDBanUnbansTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	require.NoError(t, db.SqlcClient.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET is_banned = true WHERE id = $1`, teamID))
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	response := callSetTeamBan(t, store, teamID, http.MethodDelete)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	require.False(t, teamIsBanned(t, db, teamID))
	require.Equal(t, []uuid.UUID{teamID}, authService.invalidated)

	replayed := callSetTeamBan(t, store, teamID, http.MethodDelete)
	require.Equal(t, http.StatusNoContent, replayed.Code, replayed.Body.String())
	require.False(t, teamIsBanned(t, db, teamID))
}

func TestSetTeamBanReturnsNotFoundForUnknownTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	authService := &recordingCacheAuthService{}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	banned := callSetTeamBan(t, store, uuid.New(), http.MethodPut)
	require.Equal(t, http.StatusNotFound, banned.Code, banned.Body.String())

	unbanned := callSetTeamBan(t, store, uuid.New(), http.MethodDelete)
	require.Equal(t, http.StatusNotFound, unbanned.Code, unbanned.Body.String())
	require.Empty(t, authService.invalidated)
}

func TestPutAdminTeamsTeamIDBanReportsCacheInvalidationFailure(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := createClusterAssignmentTestTeam(t, db)
	authService := &configurableCacheAuthService{invalidateErr: errors.New("cache unavailable")}
	store := &APIStore{db: db.SqlcClient, authService: authService}

	failed := callSetTeamBan(t, store, teamID, http.MethodPut)
	require.Equal(t, http.StatusInternalServerError, failed.Code, failed.Body.String())
	require.True(t, teamIsBanned(t, db, teamID))

	authService.invalidateErr = nil
	retried := callSetTeamBan(t, store, teamID, http.MethodPut)
	require.Equal(t, http.StatusNoContent, retried.Code, retried.Body.String())
	require.Equal(t, []uuid.UUID{teamID, teamID}, authService.invalidated)
}

func callSetTeamBan(t *testing.T, store *APIStore, teamID uuid.UUID, method string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), method, "/admin/teams/"+teamID.String()+"/ban", nil)
	switch method {
	case http.MethodPut:
		store.PutAdminTeamsTeamIDBan(ginCtx, teamID)
	case http.MethodDelete:
		store.DeleteAdminTeamsTeamIDBan(ginCtx, teamID)
	default:
		require.FailNow(t, "unsupported method", method)
	}
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func teamIsBanned(t *testing.T, db *testutils.Database, teamID uuid.UUID) bool {
	t.Helper()

	var banned bool
	require.NoError(t, db.SqlcClient.TestsRawSQLQuery(t.Context(),
		`SELECT is_banned FROM public.teams WHERE id = $1`,
		func(rows pgx.Rows) error {
			require.True(t, rows.Next())

			return rows.Scan(&banned)
		},
		teamID,
	))

	return banned
}

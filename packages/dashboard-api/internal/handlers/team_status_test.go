package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestGetTeamsTeamIDStatusReturnsBlockedAndBannedStatusForAMember(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	userID := uuid.New()
	teamID := testutils.CreateTestTeam(t, db)
	reason := "manual review"
	require.NoError(t, db.AuthDB.UpsertPublicUser(t.Context(), userID))
	insertHandlerTestTeamMember(t, db, userID, teamID, true)
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET is_blocked = true, is_banned = true, blocked_reason = $1 WHERE id = $2`,
		reason,
		teamID,
	))

	response := callGetTeamStatus(t, &APIStore{authDB: db.AuthDB}, teamID, &userID)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var body api.TeamStatusResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.True(t, body.IsBlocked)
	require.True(t, body.IsBanned)
	require.Equal(t, &reason, body.BlockedReason)
}

func TestGetTeamsTeamIDStatusDoesNotRevealTeamsOutsideUserMembership(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	userID := uuid.New()
	teamID := testutils.CreateTestTeam(t, db)
	require.NoError(t, db.AuthDB.UpsertPublicUser(t.Context(), userID))

	response := callGetTeamStatus(t, &APIStore{authDB: db.AuthDB}, teamID, &userID)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
}

func TestGetTeamsTeamIDStatusAllowsAnAdminToReadABannedTeam(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		`UPDATE public.teams SET is_banned = true WHERE id = $1`,
		teamID,
	))

	response := callGetTeamStatus(t, &APIStore{authDB: db.AuthDB}, teamID, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var body api.TeamStatusResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.False(t, body.IsBlocked)
	require.True(t, body.IsBanned)
	require.Nil(t, body.BlockedReason)
}

func callGetTeamStatus(t *testing.T, store *APIStore, teamID uuid.UUID, userID *uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/teams/"+teamID.String()+"/status", nil)
	if userID != nil {
		auth.SetUserIDForTest(t, ginCtx, *userID)
	}

	store.GetTeamsTeamIDStatus(ginCtx, teamID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

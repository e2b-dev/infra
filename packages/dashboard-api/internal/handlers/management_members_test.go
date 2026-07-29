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

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/management"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// The three routes share an implementation, so what differs is only what each
// turns a failure into.
func TestMembershipRoutesReportAnUnknownProject(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store := newMembershipStore(db)
	unknown, userID := uuid.New(), uuid.New()

	upsert := callMemberUpsert(t, store, unknown, userID, `{}`)
	require.Equal(t, http.StatusNotFound, upsert.Code, upsert.Body.String())

	// 404 on the delete too, though an absent project arguably satisfies it
	// already. The caller reads that as convergence when deleting.
	remove := callMemberDelete(t, store, unknown, userID)
	require.Equal(t, http.StatusNotFound, remove.Code, remove.Body.String())

	batch := callMemberBatch(t, store, unknown,
		`[{"user_id":"`+userID.String()+`","present":true}]`)
	require.Equal(t, http.StatusNotFound, batch.Code, batch.Body.String())
}

func TestMemberRoutesConvergeOnTheStatedPresence(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store := newMembershipStore(db)
	userID := uuid.New()

	require.Equal(t, http.StatusNoContent, callMemberUpsert(t, store, teamID, userID, `{}`).Code)
	require.Equal(t, http.StatusNoContent, callMemberUpsert(t, store, teamID, userID, `{}`).Code)
	require.Equal(t, http.StatusNoContent, callMemberDelete(t, store, teamID, userID).Code)
	require.Equal(t, http.StatusNoContent, callMemberDelete(t, store, teamID, userID).Code)
}

// The only field this handler reads out of a body, and sent rarely enough that
// a parsing mistake would go unnoticed.
func TestMemberUpsertRecordsAddedBy(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store := newMembershipStore(db)
	userID, actor := uuid.New(), uuid.New()

	recorder := callMemberUpsert(t, store, teamID, userID, `{"added_by":"`+actor.String()+`"}`)
	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())

	var addedBy *uuid.UUID
	require.NoError(t, db.AuthDB.TestsRawSQLQuery(t.Context(),
		"SELECT added_by FROM public.users_teams WHERE team_id = $1 AND user_id = $2",
		func(rows pgx.Rows) error {
			rows.Next()

			return rows.Scan(&addedBy)
		}, teamID, userID))

	require.NotNil(t, addedBy)
	require.Equal(t, actor, *addedBy)
}

// A caller sending no body is declining to name an actor, not sending a broken
// request.
func TestMemberUpsertAcceptsAnAbsentBody(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store := newMembershipStore(db)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	userID := uuid.New()
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/v1/management/projects/"+teamID.String()+"/members/"+userID.String(), nil)

	store.ManagementUpsertProjectMember(ginCtx, teamID, userID)
	ginCtx.Writer.WriteHeaderNow()

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
}

func TestBatchAppliesBothDirections(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store := newMembershipStore(db)
	leaving, joining := uuid.New(), uuid.New()

	require.Equal(t, http.StatusNoContent, callMemberUpsert(t, store, teamID, leaving, `{}`).Code)

	recorder := callMemberBatch(t, store, teamID,
		`[{"user_id":"`+joining.String()+`","present":true},`+
			`{"user_id":"`+leaving.String()+`","present":false}]`)
	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
}

// Order-independence is untrue of a request stating a user is both present and
// absent, and picking a winner would hide the caller's bug.
func TestBatchRejectsContradictoryEntries(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	teamID := testutils.CreateTestTeam(t, db)
	store := newMembershipStore(db)
	userID := uuid.New()

	recorder := callMemberBatch(t, store, teamID,
		`[{"user_id":"`+userID.String()+`","present":true},`+
			`{"user_id":"`+userID.String()+`","present":false}]`)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
}

func TestSplitBatchEntries(t *testing.T) {
	t.Parallel()

	first, second, third := uuid.New(), uuid.New(), uuid.New()

	t.Run("partitions by stated presence", func(t *testing.T) {
		t.Parallel()

		present, absent, err := splitBatchEntries(api.ManagementMemberBatchRequest{
			{UserId: first, Present: true},
			{UserId: second, Present: false},
			{UserId: third, Present: true},
		})

		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{first, third}, present)
		require.Equal(t, []uuid.UUID{second}, absent)
	})

	// Stated twice the same way says nothing new. Only disagreement is
	// unanswerable.
	t.Run("tolerates a consistent repeat", func(t *testing.T) {
		t.Parallel()

		present, absent, err := splitBatchEntries(api.ManagementMemberBatchRequest{
			{UserId: first, Present: true},
			{UserId: first, Present: true},
		})

		require.NoError(t, err)
		require.Equal(t, []uuid.UUID{first}, present)
		require.Empty(t, absent)
	})

	t.Run("refuses a contradiction", func(t *testing.T) {
		t.Parallel()

		_, _, err := splitBatchEntries(api.ManagementMemberBatchRequest{
			{UserId: first, Present: true},
			{UserId: first, Present: false},
		})

		require.ErrorIs(t, err, errContradictoryEntries)
	})

	t.Run("handles an empty request", func(t *testing.T) {
		t.Parallel()

		present, absent, err := splitBatchEntries(api.ManagementMemberBatchRequest{})

		require.NoError(t, err)
		require.Empty(t, present)
		require.Empty(t, absent)
	})
}

// Most calls find nothing, which is success rather than a 404 — the contract
// does not declare one.
func TestPurgeUserSucceedsWithNothingToPurge(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store := newMembershipStore(db)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	userID := uuid.New()
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
		"/v1/management/users/"+userID.String(), nil)

	store.ManagementPurgeUser(ginCtx, userID)
	ginCtx.Writer.WriteHeaderNow()

	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
}

func newMembershipStore(db *testutils.Database) *APIStore {
	auth := &recordingCacheAuthService{}

	return &APIStore{
		authDB:            db.AuthDB,
		authService:       auth,
		managementService: management.NewService(db.AuthDB, auth),
	}
}

func callMemberUpsert(t *testing.T, store *APIStore, teamID, userID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/v1/management/projects/"+teamID.String()+"/members/"+userID.String(), strings.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store.ManagementUpsertProjectMember(ginCtx, teamID, userID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func callMemberDelete(t *testing.T, store *APIStore, teamID, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
		"/v1/management/projects/"+teamID.String()+"/members/"+userID.String(), nil)

	store.ManagementDeleteProjectMember(ginCtx, teamID, userID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func callMemberBatch(t *testing.T, store *APIStore, teamID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/management/projects/"+teamID.String()+"/members/batch", strings.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store.ManagementBatchSyncProjectMembers(ginCtx, teamID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

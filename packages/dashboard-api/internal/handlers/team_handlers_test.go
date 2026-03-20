package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func TestMapAddTeamMemberRows(t *testing.T) {
	t.Parallel()

	status, message, ok := mapAddTeamMemberRows(0)
	if !ok {
		t.Fatalf("expected zero rows to be handled")
	}
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}
	if message != "User is already a member of this team" {
		t.Fatalf("unexpected message: %s", message)
	}
}

func TestMapRemoveTeamMemberRows(t *testing.T) {
	t.Parallel()

	status, message, ok := mapRemoveTeamMemberRows(0)
	if !ok {
		t.Fatalf("expected zero rows to be handled")
	}
	if status != 400 {
		t.Fatalf("expected status 400, got %d", status)
	}
	if message != "User is not a member of this team" {
		t.Fatalf("unexpected message: %s", message)
	}
}

func TestParseUpdateTeamBody_ProfilePictureNullClearsValue(t *testing.T) {
	t.Parallel()

	body, err := parseUpdateTeamBody(strings.NewReader(`{"profilePictureUrl":null}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !body.ProfilePictureUrlSet {
		t.Fatalf("expected profilePictureUrl to be marked as set")
	}
	if body.ProfilePictureUrl != nil {
		t.Fatalf("expected nil profilePictureUrl for explicit null")
	}
}

func TestParseUpdateTeamBody_ProfilePictureOmittedIsNoop(t *testing.T) {
	t.Parallel()

	body, err := parseUpdateTeamBody(strings.NewReader(`{"name":"team-a"}`))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if body.ProfilePictureUrlSet {
		t.Fatalf("expected profilePictureUrl to be unset when omitted")
	}
}

func TestParseUpdateTeamBody_NameNullRejected(t *testing.T) {
	t.Parallel()

	_, err := parseUpdateTeamBody(strings.NewReader(`{"name":null}`))
	if err == nil {
		t.Fatalf("expected error for null name")
	}
}

func TestRequireAuthedTeamMatchesPath_Success(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	teamID := uuid.New()
	auth.SetTeamInfo(ctx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{}
	_, ok := store.requireAuthedTeamMatchesPath(ctx, teamID)
	if !ok {
		t.Fatalf("expected team parity check to pass")
	}
}

func TestRequireAuthedTeamMatchesPath_Mismatch(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	auth.SetTeamInfo(ctx, &authtypes.Team{
		Team: &authqueries.Team{ID: uuid.New()},
	})

	store := &APIStore{}
	_, ok := store.requireAuthedTeamMatchesPath(ctx, uuid.New())
	if ok {
		t.Fatalf("expected team parity check to fail")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", recorder.Code)
	}
}

func TestDeleteTeamsTeamIDMembersUserId_RechecksDefaultAfterLock(t *testing.T) {
	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	targetUserID := createHandlerTestUser(t, testDB)
	otherUserID := createHandlerTestUser(t, testDB)

	insertHandlerTestTeamMember(t, testDB, targetUserID, teamID, false)
	insertHandlerTestTeamMember(t, testDB, otherUserID, teamID, true)

	_, tx, err := testDB.SqlcClient.WithTx(ctx)
	if err != nil {
		t.Fatalf("failed to start locking transaction: %v", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var lockedUserID uuid.UUID
	err = tx.QueryRow(
		ctx,
		`SELECT user_id
		FROM public.users_teams
		WHERE team_id = $1 AND user_id = $2
		FOR UPDATE`,
		teamID,
		targetUserID,
	).Scan(&lockedUserID)
	if err != nil {
		t.Fatalf("failed to lock target team member: %v", err)
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{db: testDB.SqlcClient}
	done := make(chan struct{})

	go func() {
		store.DeleteTeamsTeamIDMembersUserId(ginCtx, teamID, targetUserID)
		close(done)
	}()

	select {
	case <-done:
		t.Fatalf("expected delete handler to wait for the locked member row")
	case <-time.After(100 * time.Millisecond):
	}

	_, err = tx.Exec(
		ctx,
		`UPDATE public.users_teams
		SET is_default = true
		WHERE team_id = $1 AND user_id = $2`,
		teamID,
		targetUserID,
	)
	if err != nil {
		t.Fatalf("failed to promote target team member to default: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("failed to commit locking transaction: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delete handler did not finish after releasing the lock")
	}

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Cannot remove a default team member") {
		t.Fatalf("unexpected response body: %s", recorder.Body.String())
	}

	relation, err := testDB.SqlcClient.GetTeamMemberRelation(ctx, queries.GetTeamMemberRelationParams{
		TeamID: teamID,
		UserID: targetUserID,
	})
	if err != nil {
		t.Fatalf("expected target team member relation to remain, got %v", err)
	}
	if !relation.IsDefault {
		t.Fatal("expected target team member to remain marked as default")
	}
}

func createHandlerTestUser(t *testing.T, db *testutils.Database) uuid.UUID {
	t.Helper()

	userID := uuid.New()
	email := "user-" + userID.String() + "@example.com"

	err := db.AuthDb.TestsRawSQL(t.Context(), `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, email)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	return userID
}

func insertHandlerTestTeamMember(t *testing.T, db *testutils.Database, userID, teamID uuid.UUID, isDefault bool) {
	t.Helper()

	err := db.AuthDb.TestsRawSQL(t.Context(), `
INSERT INTO public.users_teams (user_id, team_id, is_default)
VALUES ($1, $2, $3)
`, userID, teamID, isDefault)
	if err != nil {
		t.Fatalf("failed to create team member relation: %v", err)
	}
}

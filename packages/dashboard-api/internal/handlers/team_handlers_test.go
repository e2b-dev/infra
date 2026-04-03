package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/teambilling"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

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

func TestPostTeamsTeamIDMembers_DuplicateMemberReturnsBadRequest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	targetUserID := createHandlerTestUser(t, testDB)
	addedByUserID := createHandlerTestUser(t, testDB)

	insertHandlerTestTeamMember(t, testDB, targetUserID, teamID, false)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	body := `{"email":"` + handlerTestUserEmail(targetUserID) + `"}`
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	ginCtx.Request = request

	auth.SetUserID(ginCtx, addedByUserID)
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{db: testDB.SqlcClient}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "User is already a member of this team") {
		t.Fatalf("unexpected response body: %s", recorder.Body.String())
	}
}

func TestDeleteTeamsTeamIDMembersUserId_NonMemberReturnsBadRequest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodDelete, "/", nil)
	auth.SetTeamInfo(ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{db: testDB.SqlcClient}
	store.DeleteTeamsTeamIDMembersUserId(ginCtx, teamID, uuid.New())

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "User is not a member of this team") {
		t.Fatalf("unexpected response body: %s", recorder.Body.String())
	}
}

func TestDeleteTeamsTeamIDMembersUserId_RechecksDefaultAfterLock(t *testing.T) {
	t.Parallel()

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
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodDelete, "/", nil)
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
	email := handlerTestUserEmail(userID)

	err := db.AuthDb.TestsRawSQL(t.Context(), `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, email)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	return userID
}

func handlerTestUserEmail(userID uuid.UUID) string {
	return "user-" + userID.String() + "@example.com"
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

func TestPostUsersBootstrap_CreatesDefaultTeamAndCallsSink(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{}

	existingTeam, err := testDB.SqlcClient.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected trigger-created default team: %v", err)
	}
	if err := testDB.SqlcClient.DeleteTeamByID(ctx, existingTeam.ID); err != nil {
		t.Fatalf("failed to remove trigger-created default team: %v", err)
	}
	if err := testDB.SqlcClient.DeletePublicUser(ctx, userID); err != nil {
		t.Fatalf("failed to remove trigger-created public user: %v", err)
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	auth.SetUserID(ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDb,
		teamProvisionSink: sink,
	}
	store.PostUsersBootstrap(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	team, err := testDB.SqlcClient.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected default team to be created: %v", err)
	}

	if len(sink.requests) != 1 {
		t.Fatalf("expected one billing provisioning call, got %d", len(sink.requests))
	}

	req := sink.requests[0]
	if req.TeamID != team.ID {
		t.Fatalf("expected sink team id %s, got %s", team.ID, req.TeamID)
	}
	if req.Reason != teamprovision.ReasonDefaultSignupTeam {
		t.Fatalf("expected default signup reason, got %s", req.Reason)
	}

	var responseBody map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if responseBody["slug"] != team.Slug {
		t.Fatalf("expected slug %s, got %v", team.Slug, responseBody["slug"])
	}
}

func TestCreateTeam_NoProvisionSinkLeavesCreatedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDb,
		teamProvisionSink: &fakeTeamProvisionSink{},
	}

	team, err := store.createTeam(ctx, userID, "Acme")
	if err != nil {
		t.Fatalf("expected team creation to succeed without external provisioning, got %v", err)
	}

	rows, err := testDB.AuthDb.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		t.Fatalf("failed to query user teams: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected default team and created team, got %d rows", len(rows))
	}

	found := false
	for _, row := range rows {
		if row.Team.ID == team.ID {
			found = true
			if row.IsDefault {
				t.Fatal("expected manually created team not to be default")
			}
		}
	}
	if !found {
		t.Fatal("expected created team to remain in local state")
	}
}

func TestCreateTeam_BillingBadRequestCleansUpCreatedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)

	store := &APIStore{
		db:     testDB.SqlcClient,
		authDB: testDB.AuthDb,
		teamProvisionSink: &fakeTeamProvisionSink{
			err: &teambilling.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message:    "limit reached",
			},
		},
	}

	_, err := store.createTeam(ctx, userID, "Acme")
	if err == nil {
		t.Fatal("expected billing error")
	}

	var provisionErr *teambilling.ProvisionError
	if !errors.As(err, &provisionErr) {
		t.Fatalf("expected provisioning error, got %T: %v", err, err)
	}
	if provisionErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", provisionErr.StatusCode)
	}

	rows, err := testDB.AuthDb.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		t.Fatalf("failed to query user teams: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected only the default team to remain, got %d rows", len(rows))
	}
	if !rows[0].IsDefault {
		t.Fatal("expected remaining team to be the default team")
	}
}

type fakeTeamProvisionSink struct {
	requests []teamprovision.TeamBillingProvisionRequestedV1
	err      error
}

func (s *fakeTeamProvisionSink) ProvisionTeam(_ context.Context, req teamprovision.TeamBillingProvisionRequestedV1) error {
	s.requests = append(s.requests, req)

	return s.err
}

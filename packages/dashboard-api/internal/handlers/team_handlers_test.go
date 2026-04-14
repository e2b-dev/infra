package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
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

	return createHandlerTestUserAt(t, db, time.Now().Add(-newUserNewTeamRequireBillingMethodThreshold-time.Hour))
}

func createHandlerTestUserAt(t *testing.T, db *testutils.Database, createdAt time.Time) uuid.UUID {
	t.Helper()

	userID := uuid.New()
	email := handlerTestUserEmail(userID)

	err := db.SupabaseDB.TestsRawSQL(t.Context(), `
INSERT INTO auth.users (id, email, created_at)
VALUES ($1, $2, $3)
`, userID, email, createdAt)
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
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: sink,
	}
	store.PostAdminUsersBootstrap(ginCtx)

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

func TestPostUsersBootstrap_ProvisioningFailureKeepsCreatedDefaultTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{
		err: &internalteamprovision.ProvisionError{
			StatusCode: http.StatusInternalServerError,
			Message:    "boom",
		},
	}

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
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: sink,
	}
	store.PostAdminUsersBootstrap(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	if len(sink.requests) != 1 {
		t.Fatalf("expected one provisioning call, got %d", len(sink.requests))
	}

	team, err := testDB.SqlcClient.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected default team to remain after provisioning failure: %v", err)
	}

	rows, err := testDB.AuthDB.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		t.Fatalf("failed to query user teams: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one default team to remain, got %d rows", len(rows))
	}
	if rows[0].Team.ID != team.ID {
		t.Fatalf("expected remaining team %s, got %s", team.ID, rows[0].Team.ID)
	}
	if !rows[0].IsDefault {
		t.Fatal("expected remaining team to be the default team")
	}
}

func TestBootstrapUser_ConcurrentRequestsCreateSingleDefaultTeam(t *testing.T) {
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

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: sink,
	}

	var wg sync.WaitGroup
	results := make(chan provisionedTeam, 2)
	errs := make(chan error, 2)

	for range 2 {
		wg.Go(func() {
			team, err := store.bootstrapUser(ctx, userID)
			if err != nil {
				errs <- err

				return
			}

			results <- team
		})
	}

	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("expected bootstrap to succeed, got %v", err)
		}
	}

	var teamIDs []uuid.UUID
	for team := range results {
		teamIDs = append(teamIDs, team.ID)
	}
	if len(teamIDs) != 2 {
		t.Fatalf("expected two bootstrap results, got %d", len(teamIDs))
	}
	if teamIDs[0] != teamIDs[1] {
		t.Fatalf("expected both bootstrap requests to resolve to the same team, got %s and %s", teamIDs[0], teamIDs[1])
	}

	var defaultTeamCount int
	err = testDB.AuthDB.TestsRawSQLQuery(ctx,
		`SELECT count(*)
		FROM public.users_teams
		WHERE user_id = $1 AND is_default = true`,
		func(rows pgx.Rows) error {
			if !rows.Next() {
				return errors.New("missing default team count row")
			}

			return rows.Scan(&defaultTeamCount)
		},
		userID,
	)
	if err != nil {
		t.Fatalf("failed to count default team memberships: %v", err)
	}
	if defaultTeamCount != 1 {
		t.Fatalf("expected exactly one default team membership, got %d", defaultTeamCount)
	}
}

func TestCreateTeam_RecentUserCreatesBlockedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUserAt(t, testDB, time.Now().Add(-time.Hour))

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
	}

	team, err := store.createTeam(ctx, userID, "Acme")
	if err != nil {
		t.Fatalf("expected team creation to succeed for recent user, got %v", err)
	}
	if !team.IsBlocked {
		t.Fatal("expected recent user team to be blocked")
	}
	if team.BlockedReason == nil || *team.BlockedReason != blockedReasonMissingPayment {
		t.Fatalf("expected blocked reason %q, got %v", blockedReasonMissingPayment, team.BlockedReason)
	}
}

func TestPostTeams_LocalPolicyDeniedReturnsBadRequestWithoutCreatingTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{}

	for range 2 {
		team, err := testDB.SqlcClient.CreateTeam(ctx, queries.CreateTeamParams{
			Name:  "extra",
			Tier:  baseTierID,
			Email: handlerTestUserEmail(userID),
		})
		if err != nil {
			t.Fatalf("failed to create extra team: %v", err)
		}
		if err := testDB.SqlcClient.CreateTeamMembership(ctx, queries.CreateTeamMembershipParams{
			UserID:    userID,
			TeamID:    team.ID,
			IsDefault: false,
			AddedBy:   &userID,
		}); err != nil {
			t.Fatalf("failed to attach extra team membership: %v", err)
		}
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"name":"Acme"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetUserID(ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: sink,
	}
	store.PostTeams(ginCtx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	if len(sink.requests) != 0 {
		t.Fatalf("expected no provisioning call, got %d", len(sink.requests))
	}

	rows, err := testDB.AuthDB.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		t.Fatalf("failed to query user teams: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected existing teams to remain unchanged, got %d rows", len(rows))
	}
}

func TestPostTeams_InvalidNameReturnsBadRequest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)

	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"   "}`} {
		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(body))
		ginCtx.Request.Header.Set("Content-Type", "application/json")
		auth.SetUserID(ginCtx, userID)

		sink := &fakeTeamProvisionSink{}
		store := &APIStore{
			db:                testDB.SqlcClient,
			authDB:            testDB.AuthDB,
			supabaseDB:        testDB.SupabaseDB,
			teamProvisionSink: sink,
		}
		store.PostTeams(ginCtx)

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for body %s, got %d", body, recorder.Code)
		}
		if len(sink.requests) != 0 {
			t.Fatalf("expected no provisioning call for body %s, got %d", body, len(sink.requests))
		}
	}
}

func TestPostTeams_InvalidRequestBodyReturnsBadRequest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"name":`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetUserID(ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: sink,
	}
	store.PostTeams(ginCtx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("PostTeams(invalid JSON) status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "Invalid request body") {
		t.Fatalf("PostTeams(invalid JSON) body = %q, want message containing %q", recorder.Body.String(), "Invalid request body")
	}
	if len(sink.requests) != 0 {
		t.Fatalf("PostTeams(invalid JSON) provisioning calls = %d, want %d", len(sink.requests), 0)
	}
}

func TestPostTeams_TrimsNameBeforeCreate(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"name":"  Acme  "}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetUserID(ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: sink,
	}
	store.PostTeams(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	rows, err := testDB.AuthDB.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		t.Fatalf("failed to query user teams: %v", err)
	}

	foundCreatedTeam := false
	for _, row := range rows {
		if row.IsDefault {
			continue
		}

		foundCreatedTeam = true
		if row.Team.Name != "Acme" {
			t.Fatalf("expected trimmed team name %q, got %q", "Acme", row.Team.Name)
		}
	}
	if !foundCreatedTeam {
		t.Fatal("expected created team to exist")
	}
}

func TestPostTeams_ProvisioningFailureRollsBackCreatedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{
		err: &internalteamprovision.ProvisionError{
			StatusCode: http.StatusBadRequest,
			Message:    "limit reached",
		},
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"name":"Acme"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")
	auth.SetUserID(ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: sink,
	}
	store.PostTeams(ginCtx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	if len(sink.requests) != 1 {
		t.Fatalf("expected one provisioning call, got %d", len(sink.requests))
	}

	rows, err := testDB.AuthDB.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
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

func TestPostTeams_ProvisioningFailurePreservesProvisionErrorStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		message string
	}{
		{name: "too_many_requests", status: http.StatusTooManyRequests, message: "rate limited"},
		{name: "service_unavailable", status: http.StatusServiceUnavailable, message: "billing unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testDB := testutils.SetupDatabase(t)
			ctx := t.Context()
			userID := createHandlerTestUser(t, testDB)
			sink := &fakeTeamProvisionSink{
				err: &internalteamprovision.ProvisionError{
					StatusCode: tt.status,
					Message:    tt.message,
				},
			}

			recorder := httptest.NewRecorder()
			ginCtx, _ := gin.CreateTestContext(recorder)
			ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"name":"Acme"}`))
			ginCtx.Request.Header.Set("Content-Type", "application/json")
			auth.SetUserID(ginCtx, userID)

			store := &APIStore{
				db:                testDB.SqlcClient,
				authDB:            testDB.AuthDB,
				supabaseDB:        testDB.SupabaseDB,
				teamProvisionSink: sink,
			}
			store.PostTeams(ginCtx)

			if recorder.Code != tt.status {
				t.Fatalf("PostTeams(provision status %d) status = %d, want %d", tt.status, recorder.Code, tt.status)
			}
			if len(sink.requests) != 1 {
				t.Fatalf("PostTeams(provision status %d) provisioning calls = %d, want %d", tt.status, len(sink.requests), 1)
			}

			var responseBody map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &responseBody); err != nil {
				t.Fatalf("json.Unmarshal(PostTeams response) error = %v, want nil", err)
			}

			codeValue, ok := responseBody["code"].(float64)
			if !ok {
				t.Fatalf("PostTeams(provision status %d) response code type = %T, want float64", tt.status, responseBody["code"])
			}
			if got := int(codeValue); got != tt.status {
				t.Fatalf("PostTeams(provision status %d) response code = %d, want %d", tt.status, got, tt.status)
			}

			messageValue, ok := responseBody["message"].(string)
			if !ok {
				t.Fatalf("PostTeams(provision status %d) response message type = %T, want string", tt.status, responseBody["message"])
			}
			if messageValue != tt.message {
				t.Fatalf("PostTeams(provision status %d) response message = %q, want %q", tt.status, messageValue, tt.message)
			}

			rows, err := testDB.AuthDB.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
			if err != nil {
				t.Fatalf("GetTeamsWithUsersTeamsWithTier(userID=%s) error = %v, want nil", userID, err)
			}
			if len(rows) != 1 {
				t.Fatalf("GetTeamsWithUsersTeamsWithTier(userID=%s) rows = %d, want %d", userID, len(rows), 1)
			}
			if !rows[0].IsDefault {
				t.Fatal("expected remaining team to be the default team")
			}
		})
	}
}

func TestCreateTeam_ConcurrentRequestsRespectLocalPolicyWithZeroMemberships(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)

	existingTeam, err := testDB.SqlcClient.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected trigger-created default team: %v", err)
	}
	if err := testDB.SqlcClient.DeleteTeamByID(ctx, existingTeam.ID); err != nil {
		t.Fatalf("failed to remove default team: %v", err)
	}

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		supabaseDB:        testDB.SupabaseDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
	}

	var wg sync.WaitGroup
	results := make(chan error, 4)

	for _, name := range []string{"Acme-1", "Acme-2", "Acme-3", "Acme-4"} {
		wg.Add(1)
		go func(teamName string) {
			defer wg.Done()
			_, err := store.createTeam(ctx, userID, teamName)
			results <- err
		}(name)
	}

	wg.Wait()
	close(results)

	var successCount int
	var badRequestCount int
	for err := range results {
		if err == nil {
			successCount++

			continue
		}

		var provisionErr *internalteamprovision.ProvisionError
		if !errors.As(err, &provisionErr) {
			t.Fatalf("expected provisioning error, got %T: %v", err, err)
		}
		if provisionErr.StatusCode == http.StatusBadRequest {
			badRequestCount++

			continue
		}

		t.Fatalf("expected bad request or success, got %d", provisionErr.StatusCode)
	}

	if successCount != maxTeamsPerUser {
		t.Fatalf("expected %d successes, got %d", maxTeamsPerUser, successCount)
	}
	if badRequestCount != 1 {
		t.Fatalf("expected one bad request, got %d", badRequestCount)
	}
}

type fakeTeamProvisionSink struct {
	mu       sync.Mutex
	requests []teamprovision.TeamBillingProvisionRequestedV1
	err      error
}

func (s *fakeTeamProvisionSink) ProvisionTeam(_ context.Context, req teamprovision.TeamBillingProvisionRequestedV1) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, req)

	return s.err
}

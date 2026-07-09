package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/identity"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/provisioning"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

const testBaseTier = "base_v1"

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
	auth.SetTeamInfoForTest(t, ctx, &authtypes.Team{
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

	auth.SetTeamInfoForTest(t, ctx, &authtypes.Team{
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

	auth.SetUserIDForTest(t, ginCtx, addedByUserID)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{
		db:              testDB.SqlcClient,
		authDB:          testDB.AuthDB,
		identityService: handlerTestIdentityProvider{},
	}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "User is already a member of this team") {
		t.Fatalf("unexpected response body: %s", recorder.Body.String())
	}
}

func TestPostTeamsTeamIDMembers_CreatesPublicUserAnchorForInvitee(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	inviteeID := uuid.New()
	addedByUserID := createHandlerTestUser(t, testDB)
	inviteeEmail := handlerTestUserEmail(inviteeID)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	body := `{"email":"` + inviteeEmail + `"}`
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	ginCtx.Request = request

	auth.SetUserIDForTest(t, ginCtx, addedByUserID)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{
		db:              testDB.SqlcClient,
		authDB:          testDB.AuthDB,
		authService:     noopAuthService{},
		identityService: handlerTestIdentityProvider{},
	}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if ginCtx.Writer.Status() != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", ginCtx.Writer.Status(), recorder.Body.String())
	}

	if _, err := testDB.SqlcClient.GetPublicUserID(ctx, inviteeID); err != nil {
		t.Fatalf("expected public user anchor to be created: %v", err)
	}

	if _, err := testDB.SqlcClient.GetTeamMemberRelation(ctx, queries.GetTeamMemberRelationParams{
		TeamID: teamID,
		UserID: inviteeID,
	}); err != nil {
		t.Fatalf("expected invitee team member relation: %v", err)
	}
}

type ssoIdentityProvider struct {
	handlerTestIdentityProvider

	orgByUser map[uuid.UUID]uuid.UUID
}

func (p ssoIdentityProvider) UserOrganizationID(_ context.Context, userID uuid.UUID) (uuid.UUID, error) {
	return p.orgByUser[userID], nil
}

func TestPostTeamsTeamIDMembers_RejectsInviteOutsideSSOOrg(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	teamID := uuid.New()
	orgID := uuid.New()
	actingUserID := uuid.New()
	inviteeID := uuid.New()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	auth.SetUserIDForTest(t, ginCtx, actingUserID)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID, SsoOrganizationID: &orgID},
	})
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"email":"`+handlerTestUserEmail(inviteeID)+`"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	provisioningService := provisioning.New(nil, ssoIdentityProvider{}, &fakeTeamProvisionSink{})

	store := &APIStore{
		identityService:     ssoIdentityProvider{},
		provisioningService: provisioningService,
	}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an invite outside the SSO org, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPostTeamsTeamIDMembers_AllowsInviteFromSSOOrg(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	orgID := uuid.New()
	teamID := testutils.CreateTestTeam(t, testDB)
	actingUserID := createHandlerTestUser(t, testDB)
	inviteeID := uuid.New()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	auth.SetUserIDForTest(t, ginCtx, actingUserID)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID, SsoOrganizationID: &orgID},
	})
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"email":"`+handlerTestUserEmail(inviteeID)+`"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	profiles := ssoIdentityProvider{orgByUser: map[uuid.UUID]uuid.UUID{inviteeID: orgID}}
	provisioningService := provisioning.New(testDB.AuthDB, profiles, &fakeTeamProvisionSink{})

	store := &APIStore{
		db:                  testDB.SqlcClient,
		authDB:              testDB.AuthDB,
		authService:         noopAuthService{},
		identityService:     profiles,
		provisioningService: provisioningService,
	}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if ginCtx.Writer.Status() != http.StatusCreated {
		t.Fatalf("expected 201 for an in-org invite, got %d: %s", ginCtx.Writer.Status(), recorder.Body.String())
	}

	memberships, err := testDB.AuthDB.Read.GetTeamsWithUsersTeams(ctx, inviteeID)
	if err != nil {
		t.Fatalf("failed to read memberships: %v", err)
	}
	if len(memberships) != 1 || memberships[0].Team.ID != teamID {
		t.Fatalf("expected invitee to be a member of %s, got %v", teamID, memberships)
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
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
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
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
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
		SET is_default = false
		WHERE user_id = $1 AND is_default = true`,
		targetUserID,
	)
	if err != nil {
		t.Fatalf("failed to clear target default team member: %v", err)
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

	if err := db.AuthDB.Write.UpsertPublicUser(t.Context(), userID); err != nil {
		t.Fatalf("failed to create public user: %v", err)
	}

	team, err := db.AuthDB.Write.CreateTeam(t.Context(), authqueries.CreateTeamParams{
		Name:  email,
		Tier:  testBaseTier,
		Email: email,
	})
	if err != nil {
		t.Fatalf("failed to create default team: %v", err)
	}

	if err := db.AuthDB.Write.CreateTeamMembership(t.Context(), authqueries.CreateTeamMembershipParams{
		UserID:    userID,
		TeamID:    team.ID,
		IsDefault: true,
		AddedBy:   nil,
	}); err != nil {
		t.Fatalf("failed to create default team membership: %v", err)
	}

	return userID
}

func handlerTestUserEmail(userID uuid.UUID) string {
	return "user-" + userID.String() + "@example.com"
}

func newPostTeamsTestStore(t *testing.T, db *testutils.Database, sink *fakeTeamProvisionSink) *APIStore {
	t.Helper()

	provisioningService := provisioning.New(db.AuthDB, handlerTestIdentityProvider{}, sink)

	return &APIStore{
		db:                  db.SqlcClient,
		authDB:              db.AuthDB,
		provisioningService: provisioningService,
	}
}

type handlerTestIdentityProvider struct{}

func (handlerTestIdentityProvider) ProfilesByUserID(_ context.Context, userIDs []uuid.UUID) (map[uuid.UUID]identity.Profile, error) {
	profiles := make(map[uuid.UUID]identity.Profile, len(userIDs))
	for _, userID := range userIDs {
		if userID == uuid.Nil {
			continue
		}
		profiles[userID] = identity.Profile{
			UserID: userID,
			Email:  handlerTestUserEmail(userID),
		}
	}

	return profiles, nil
}

func (handlerTestIdentityProvider) FindProfilesByEmail(_ context.Context, email string) ([]identity.Profile, error) {
	userIDText := strings.TrimSuffix(strings.TrimPrefix(email, "user-"), "@example.com")
	userID, _ := uuid.Parse(userIDText)
	if userID == uuid.Nil {
		return []identity.Profile{}, nil
	}

	return []identity.Profile{{
		UserID: userID,
		Email:  email,
	}}, nil
}

func (handlerTestIdentityProvider) TeamCreatorContext(context.Context, uuid.UUID) (*teamprovision.CreatorContextV1, error) {
	return nil, nil
}

func (handlerTestIdentityProvider) IdentityOrganizationID(context.Context, string, string) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (handlerTestIdentityProvider) UserOrganizationID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (handlerTestIdentityProvider) SetIdentityExternalID(context.Context, string, string, uuid.UUID) error {
	return nil
}

func (handlerTestIdentityProvider) PrepareDeleteUser(context.Context, uuid.UUID) (identity.DeleteUserHandle, error) {
	return nil, nil
}

func insertHandlerTestTeamMember(t *testing.T, db *testutils.Database, userID, teamID uuid.UUID, isDefault bool) {
	t.Helper()

	if isDefault {
		if err := db.AuthDB.TestsRawSQL(t.Context(), `
UPDATE public.users_teams
SET is_default = false
WHERE user_id = $1
`, userID); err != nil {
			t.Fatalf("failed to clear default team member relation: %v", err)
		}
	}

	err := db.AuthDB.TestsRawSQL(t.Context(), `
INSERT INTO public.users_teams (user_id, team_id, is_default)
VALUES ($1, $2, $3)
`, userID, teamID, isDefault)
	if err != nil {
		t.Fatalf("failed to create team member relation: %v", err)
	}
}

func TestPostAdminUsersBootstrap_EmptyOIDCUserIDReturnsBadRequest(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"oidc_issuer":"https://ory.example.test","oidc_user_id":"   ","oidc_user_email":"ada@example.test","oidc_user_name":null}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{}
	store.PostAdminUsersBootstrap(ginCtx)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for blank oidc_user_id, got %d", recorder.Code)
	}
}

func TestPostTeams_LocalPolicyDeniedReturnsBadRequestWithoutCreatingTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{}

	for range 2 {
		team, err := testDB.AuthDB.Write.CreateTeam(ctx, authqueries.CreateTeamParams{
			Name:  "extra",
			Tier:  testBaseTier,
			Email: handlerTestUserEmail(userID),
		})
		if err != nil {
			t.Fatalf("failed to create extra team: %v", err)
		}
		if err := testDB.AuthDB.Write.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
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
	auth.SetUserIDForTest(t, ginCtx, userID)

	store := newPostTeamsTestStore(t, testDB, sink)
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
		auth.SetUserIDForTest(t, ginCtx, userID)

		sink := &fakeTeamProvisionSink{}
		store := newPostTeamsTestStore(t, testDB, sink)
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
	auth.SetUserIDForTest(t, ginCtx, userID)

	store := newPostTeamsTestStore(t, testDB, sink)
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
	auth.SetUserIDForTest(t, ginCtx, userID)

	store := newPostTeamsTestStore(t, testDB, sink)
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
	auth.SetUserIDForTest(t, ginCtx, userID)

	store := newPostTeamsTestStore(t, testDB, sink)
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
			auth.SetUserIDForTest(t, ginCtx, userID)

			store := newPostTeamsTestStore(t, testDB, sink)
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

type noopAuthService struct{}

func (noopAuthService) ValidateAPIKey(context.Context, *gin.Context, string) (*authtypes.Team, *auth.APIError) {
	return nil, nil
}

func (noopAuthService) ValidateAccessToken(context.Context, *gin.Context, string) (uuid.UUID, *auth.APIError) {
	return uuid.Nil, nil
}

func (noopAuthService) ValidateAuthProviderToken(context.Context, *gin.Context, string) (uuid.UUID, *auth.APIError) {
	return uuid.Nil, nil
}

func (noopAuthService) ValidateAuthProviderTeam(context.Context, *gin.Context, string) (*authtypes.Team, *auth.APIError) {
	return nil, nil
}

func (noopAuthService) GetTeamByID(context.Context, uuid.UUID) (*authtypes.Team, error) {
	return nil, nil
}

func (noopAuthService) InvalidateTeamMemberCache(context.Context, uuid.UUID, string) {}

func (noopAuthService) InvalidateTeamCache(context.Context, uuid.UUID) error {
	return nil
}

func (noopAuthService) Close(context.Context) error {
	return nil
}

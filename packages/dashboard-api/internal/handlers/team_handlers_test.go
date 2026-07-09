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
	"github.com/e2b-dev/infra/packages/auth/pkg/auth/oidc"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
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
		db:           testDB.SqlcClient,
		authDB:       testDB.AuthDB,
		userProfiles: newHandlerTestUserProfiles(),
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
		db:           testDB.SqlcClient,
		authDB:       testDB.AuthDB,
		authService:  noopAuthService{},
		userProfiles: newHandlerTestUserProfiles(),
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

	createdAt := time.Now()

	return createHandlerTestUserWithCreatedAt(t, db, &createdAt)
}

func createHandlerTestUserAt(t *testing.T, db *testutils.Database, createdAt time.Time) uuid.UUID {
	t.Helper()

	return createHandlerTestUserWithCreatedAt(t, db, &createdAt)
}

func createHandlerTestUserWithCreatedAt(t *testing.T, db *testutils.Database, createdAt *time.Time) uuid.UUID {
	t.Helper()

	userID := uuid.New()
	email := handlerTestUserEmail(userID)
	_ = createdAt

	if err := db.AuthDB.Write.UpsertPublicUser(t.Context(), userID); err != nil {
		t.Fatalf("failed to create public user: %v", err)
	}

	team, err := db.AuthDB.Write.CreateTeam(t.Context(), authqueries.CreateTeamParams{
		Name:  email,
		Tier:  baseTierID,
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

type handlerTestUserProfiles struct{}

func newHandlerTestUserProfiles() userprofile.Provider {
	return handlerTestUserProfiles{}
}

func (handlerTestUserProfiles) GetProfilesByUserID(_ context.Context, userIDs []uuid.UUID) (map[uuid.UUID]userprofile.Profile, error) {
	profiles := make(map[uuid.UUID]userprofile.Profile, len(userIDs))
	for _, userID := range userIDs {
		if userID == uuid.Nil {
			continue
		}
		profiles[userID] = userprofile.Profile{
			UserID: userID,
			Email:  handlerTestUserEmail(userID),
		}
	}

	return profiles, nil
}

func (handlerTestUserProfiles) FindProfilesByEmail(_ context.Context, email string) ([]userprofile.Profile, error) {
	userIDText := strings.TrimSuffix(strings.TrimPrefix(email, "user-"), "@example.com")
	userID, _ := uuid.Parse(userIDText)
	if userID == uuid.Nil {
		return []userprofile.Profile{}, nil
	}

	return []userprofile.Profile{{
		UserID: userID,
		Email:  email,
	}}, nil
}

func (handlerTestUserProfiles) GetTeamCreatorContext(context.Context, uuid.UUID) (*teamprovision.CreatorContextV1, error) {
	return nil, nil
}

func (handlerTestUserProfiles) SetIdentityExternalID(context.Context, string, uuid.UUID) error {
	return nil
}

func (handlerTestUserProfiles) PrepareDeleteUser(context.Context, uuid.UUID) (userprofile.DeleteUserHandle, error) {
	return nil, nil
}

func TestDefaultTeamNameFromProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile userprofile.Profile
		want    string
	}{
		{
			name: "profile name",
			profile: userprofile.Profile{
				Email: "fallback@example.com",
				Name:  "ada",
			},
			want: "Ada's Default Team",
		},
		{
			name: "profile full name first word",
			profile: userprofile.Profile{
				Email: "fallback@example.com",
				Name:  "grace hopper",
			},
			want: "Grace's Default Team",
		},
		{
			name: "email prefix",
			profile: userprofile.Profile{
				Email: "barbara@example.com",
			},
			want: "Barbara's Default Team",
		},
		{
			name:    "no base name",
			profile: userprofile.Profile{},
			want:    "Default Team",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := defaultTeamNameFromProfile(tt.profile)
			if got != tt.want {
				t.Fatalf("defaultTeamNameFromProfile() = %q, want %q", got, tt.want)
			}
		})
	}
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

func TestBootstrapAuthProviderUser_CreatesIdentityAndDefaultTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	store := &APIStore{
		config: cfg.Config{
			OryIssuerURL: "https://ory.example.test",
			AuthProvider: auth.ProviderConfig{
				JWT: []oidc.Config{
					{
						Issuer: oidc.Issuer{
							URL:       "https://ory.example.test",
							Audiences: []string{"https://dashboard-api.example.test"},
						},
					},
				},
			},
		},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:      "https://ory.example.test",
		OIDCUserID:      uuid.NewString(),
		OIDCUserEmail:   "ada@example.test",
		OIDCUserName:    nil,
		SignupIP:        "198.51.100.20",
		SignupUserAgent: "Mozilla/5.0",
	}

	team, err := store.bootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("expected bootstrap to succeed: %v", err)
	}

	userIdentity, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	})
	if err != nil {
		t.Fatalf("expected user identity to be created: %v", err)
	}

	defaultTeam, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, userIdentity.UserID)
	if err != nil {
		t.Fatalf("expected default team to be created: %v", err)
	}
	if defaultTeam.ID != team.ID {
		t.Fatalf("expected response team %s, got %s", defaultTeam.ID, team.ID)
	}
	if defaultTeam.Name != "Default Team" {
		t.Fatalf("expected team name %q, got %q", "Default Team", defaultTeam.Name)
	}
	if defaultTeam.Email != "ada@example.test" {
		t.Fatalf("expected team email %q, got %q", "ada@example.test", defaultTeam.Email)
	}

	if len(sink.requests) != 1 {
		t.Fatalf("expected one billing provisioning call, got %d", len(sink.requests))
	}
	if sink.requests[0].CreatorUserID != userIdentity.UserID {
		t.Fatalf("expected sink creator %s, got %s", userIdentity.UserID, sink.requests[0].CreatorUserID)
	}
	if sink.requests[0].CreatorContext == nil {
		t.Fatal("expected sink creator context")
	}
	if sink.requests[0].CreatorContext.IPAddress != "198.51.100.20" {
		t.Fatalf("expected sink creator ip %q, got %q", "198.51.100.20", sink.requests[0].CreatorContext.IPAddress)
	}
	if sink.requests[0].CreatorContext.UserAgent != "Mozilla/5.0" {
		t.Fatalf("expected sink creator user agent %q, got %q", "Mozilla/5.0", sink.requests[0].CreatorContext.UserAgent)
	}
	if sink.requests[0].CreatorContext.AuthMethod != teamprovision.AuthMethodSocial {
		t.Fatalf("expected sink creator auth method %q, got %q", teamprovision.AuthMethodSocial, sink.requests[0].CreatorContext.AuthMethod)
	}
}

type recordingUserProfiles struct {
	handlerTestUserProfiles

	externalIDSubject string
	externalID        uuid.UUID
	externalIDCalls   int
}

func (r *recordingUserProfiles) SetIdentityExternalID(_ context.Context, subject string, externalID uuid.UUID) error {
	r.externalIDSubject = subject
	r.externalID = externalID
	r.externalIDCalls++

	return nil
}

func TestBootstrapOIDCUser_PopulatesOryExternalID(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}
	profiles := &recordingUserProfiles{}

	store := &APIStore{
		config: cfg.Config{
			OryIssuerURL: "https://ory.example.test",
		},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      profiles,
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
	}

	if _, err := store.bootstrapOIDCUser(ctx, input); err != nil {
		t.Fatalf("expected bootstrap to succeed: %v", err)
	}

	userIdentity, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	})
	if err != nil {
		t.Fatalf("expected user identity to be created: %v", err)
	}

	if profiles.externalIDCalls != 1 {
		t.Fatalf("expected one external id update, got %d", profiles.externalIDCalls)
	}
	if profiles.externalIDSubject != input.OIDCUserID {
		t.Fatalf("expected external id set on subject %q, got %q", input.OIDCUserID, profiles.externalIDSubject)
	}
	if profiles.externalID != userIdentity.UserID {
		t.Fatalf("expected external id %s, got %s", userIdentity.UserID, profiles.externalID)
	}
}

// failingUserProfiles fails the Ory external_id PATCH to simulate the IdP being
// unavailable after the bootstrap transaction has already committed.
type failingUserProfiles struct {
	handlerTestUserProfiles

	externalIDCalls int
}

func (f *failingUserProfiles) SetIdentityExternalID(context.Context, string, uuid.UUID) error {
	f.externalIDCalls++

	return errors.New("ory unavailable")
}

// A failed external_id PATCH must not roll back the committed user/identity/team.
// The PATCH now runs after the transaction commits, so the user stays provisioned
// and the next login can backfill external_id rather than being stranded.
func TestBootstrapOIDCUser_ExternalIDFailureKeepsUserProvisioned(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}
	profiles := &failingUserProfiles{}

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      profiles,
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
	}

	if _, err := store.bootstrapOIDCUser(ctx, input); err == nil {
		t.Fatal("expected bootstrap to fail when external_id patch fails")
	}
	if profiles.externalIDCalls != 1 {
		t.Fatalf("expected one external id attempt, got %d", profiles.externalIDCalls)
	}

	userIdentity, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	})
	if err != nil {
		t.Fatalf("expected user identity to survive external_id failure: %v", err)
	}
	if _, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, userIdentity.UserID); err != nil {
		t.Fatalf("expected default team to survive external_id failure: %v", err)
	}

	// The billing event must be emitted before the external_id backfill, otherwise
	// a PATCH failure leaves the committed team billing-orphaned.
	if len(sink.requests) != 1 {
		t.Fatalf("expected one billing provisioning call despite external_id failure, got %d", len(sink.requests))
	}
}

// A user provisioned by a prior bootstrap whose external_id PATCH failed re-runs
// here on the next login; the existing-team path re-asserts the PATCH and
// backfills external_id without creating a duplicate team.
func TestBootstrapOIDCUser_ReRunBackfillsExternalID(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      &failingUserProfiles{},
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
	}

	if _, err := store.bootstrapOIDCUser(ctx, input); err == nil {
		t.Fatal("expected first bootstrap to fail on external_id patch")
	}

	userIdentity, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	})
	if err != nil {
		t.Fatalf("expected user identity from first bootstrap: %v", err)
	}
	existingTeam, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, userIdentity.UserID)
	if err != nil {
		t.Fatalf("expected default team from first bootstrap: %v", err)
	}

	recording := &recordingUserProfiles{}
	store.userProfiles = recording

	secondTeam, err := store.bootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("expected re-run bootstrap to succeed: %v", err)
	}
	if secondTeam.ID != existingTeam.ID {
		t.Fatalf("expected re-run to reuse existing team %s, got %s", existingTeam.ID, secondTeam.ID)
	}
	if recording.externalIDCalls != 1 {
		t.Fatalf("expected re-run to backfill external_id once, got %d", recording.externalIDCalls)
	}
	if recording.externalID != userIdentity.UserID {
		t.Fatalf("expected external id %s, got %s", userIdentity.UserID, recording.externalID)
	}
}

func TestBootstrapOIDCUser_ConcurrentRequestsSingleIdentityAndTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	store := &APIStore{
		config: cfg.Config{
			OryIssuerURL: "https://ory.example.test",
			AuthProvider: auth.ProviderConfig{
				JWT: []oidc.Config{
					{
						Issuer: oidc.Issuer{
							URL:       "https://ory.example.test",
							Audiences: []string{"https://dashboard-api.example.test"},
						},
					},
				},
			},
		},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
		OIDCUserName:  nil,
	}

	const concurrency = 4
	var wg sync.WaitGroup
	results := make(chan provisionedTeam, concurrency)
	errs := make(chan error, concurrency)

	for range concurrency {
		wg.Go(func() {
			team, err := store.bootstrapOIDCUser(ctx, input)
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
	if len(teamIDs) != concurrency {
		t.Fatalf("expected %d bootstrap results, got %d", concurrency, len(teamIDs))
	}
	for _, id := range teamIDs[1:] {
		if id != teamIDs[0] {
			t.Fatalf("expected all bootstrap calls to share team %s, got %s", teamIDs[0], id)
		}
	}

	userIdentity, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	})
	if err != nil {
		t.Fatalf("expected single user identity to exist: %v", err)
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
		userIdentity.UserID,
	)
	if err != nil {
		t.Fatalf("failed to count default team memberships: %v", err)
	}
	if defaultTeamCount != 1 {
		t.Fatalf("expected exactly one default team for canonical user, got %d", defaultTeamCount)
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

func TestBootstrapOIDCUser_OryIssuerWithoutJWTConfigIsAccepted(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	const oryIssuer = "https://ory.example.test"

	store := &APIStore{
		config: cfg.Config{
			OryIssuerURL: oryIssuer,
		},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
	}

	team, err := store.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
		OIDCIssuer:    oryIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
		OIDCUserName:  nil,
	})
	if err != nil {
		t.Fatalf("expected bootstrap to succeed with Ory issuer but no JWT config: %v", err)
	}
	if team.ID == uuid.Nil {
		t.Fatal("expected provisioned team")
	}
}

func TestBootstrapOIDCUser_OryModeRejectsNonOryJWTIssuer(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	const oryIssuer = "https://ory.example.test"
	const otherIssuer = "https://workos.example.test"

	store := &APIStore{
		config: cfg.Config{
			OryIssuerURL: oryIssuer,
			AuthProvider: auth.ProviderConfig{
				JWT: []oidc.Config{
					{Issuer: oidc.Issuer{URL: otherIssuer}},
				},
			},
		},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
	}

	_, err := store.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
		OIDCIssuer:    otherIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
		OIDCUserName:  nil,
	})
	if err == nil {
		t.Fatal("expected ory mode to reject a non-Ory JWT issuer at bootstrap")
	}
	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected ProvisionError with status 400, got %v", err)
	}
}

func TestBootstrapOIDCUser_UnknownIssuerReturnsBadRequest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	store := &APIStore{
		config: cfg.Config{
			OryIssuerURL: "https://ory.example.test",
			AuthProvider: auth.ProviderConfig{
				JWT: []oidc.Config{
					{Issuer: oidc.Issuer{URL: "https://ory.example.test"}},
				},
			},
		},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
	}

	_, err := store.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
		OIDCIssuer:    "https://attacker.example.test",
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
		OIDCUserName:  nil,
	})
	if err == nil {
		t.Fatal("expected unknown issuer to be rejected")
	}

	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected ProvisionError with status 400, got %v", err)
	}
	if len(sink.requests) != 0 {
		t.Fatalf("expected no provisioning calls, got %d", len(sink.requests))
	}
}

func TestBootstrapUser_ConcurrentRequestsCreateSingleDefaultTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUser(t, testDB)
	sink := &fakeTeamProvisionSink{}

	existingTeam, err := testDB.AuthDB.Write.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected default team: %v", err)
	}
	if err := testDB.AuthDB.Write.DeleteTeamByID(ctx, existingTeam.ID); err != nil {
		t.Fatalf("failed to remove default team: %v", err)
	}

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
	}
	profile := bootstrapUserProfile{
		UserID:          userID,
		Email:           handlerTestUserEmail(userID),
		DefaultTeamName: defaultTeamNameFromProfile(userprofile.Profile{Email: handlerTestUserEmail(userID)}),
	}

	var wg sync.WaitGroup
	results := make(chan provisionedTeam, 2)
	errs := make(chan error, 2)

	for range 2 {
		wg.Go(func() {
			team, err := store.bootstrapUser(ctx, profile)
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

func TestCreateTeam_RecentUserCreatesUnblockedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createHandlerTestUserAt(t, testDB, time.Now().Add(-time.Hour))

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
		userProfiles:      newHandlerTestUserProfiles(),
	}

	team, err := store.createTeam(ctx, userID, "Acme")
	if err != nil {
		t.Fatalf("expected team creation to succeed for recent user, got %v", err)
	}
	if team.IsBlocked {
		t.Fatal("expected recent user team to remain unblocked")
	}
	if team.BlockedReason != nil {
		t.Fatalf("expected nil blocked reason, got %v", team.BlockedReason)
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
			Tier:  baseTierID,
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

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
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
		auth.SetUserIDForTest(t, ginCtx, userID)

		sink := &fakeTeamProvisionSink{}
		store := &APIStore{
			db:                testDB.SqlcClient,
			authDB:            testDB.AuthDB,
			teamProvisionSink: sink,
			userProfiles:      newHandlerTestUserProfiles(),
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
	auth.SetUserIDForTest(t, ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
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
	auth.SetUserIDForTest(t, ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
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
	auth.SetUserIDForTest(t, ginCtx, userID)

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      newHandlerTestUserProfiles(),
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
			auth.SetUserIDForTest(t, ginCtx, userID)

			store := &APIStore{
				db:                testDB.SqlcClient,
				authDB:            testDB.AuthDB,
				teamProvisionSink: sink,
				userProfiles:      newHandlerTestUserProfiles(),
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

	existingTeam, err := testDB.AuthDB.Write.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected default team: %v", err)
	}
	if err := testDB.AuthDB.Write.DeleteTeamByID(ctx, existingTeam.ID); err != nil {
		t.Fatalf("failed to remove default team: %v", err)
	}

	store := &APIStore{
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
		userProfiles:      newHandlerTestUserProfiles(),
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

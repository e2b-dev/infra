package handlers

import (
	"context"
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
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// ssoUserProfiles is a Provider whose SSO-organization lookups are configurable,
// so tests can simulate identities that belong to an Ory organization.
type ssoUserProfiles struct {
	handlerTestUserProfiles

	orgBySubject map[string]uuid.UUID
	orgByUser    map[uuid.UUID]uuid.UUID
}

func (p ssoUserProfiles) GetIdentityOrganizationID(_ context.Context, subject string) (uuid.UUID, error) {
	return p.orgBySubject[subject], nil
}

func (p ssoUserProfiles) GetUserOrganizationID(_ context.Context, userID uuid.UUID) (uuid.UUID, error) {
	return p.orgByUser[userID], nil
}

func setTeamSSOOrg(t *testing.T, db *testutils.Database, teamID, orgID uuid.UUID, autoJoin bool, createdAt time.Time) {
	t.Helper()

	if err := db.SqlcClient.TestsRawSQL(t.Context(),
		"UPDATE public.teams SET sso_organization_id = $1, sso_auto_join = $2, created_at = $3 WHERE id = $4",
		orgID, autoJoin, createdAt, teamID,
	); err != nil {
		t.Fatalf("failed to set team sso org: %v", err)
	}
}

func TestBootstrapOIDCUser_SSOJoinsMappedTeams(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	orgID := uuid.New()
	subject := uuid.NewString()

	// teamOlder is given an earlier created_at, so it is returned as the landing team.
	teamNewer := testutils.CreateTestTeam(t, testDB)
	teamOlder := testutils.CreateTestTeam(t, testDB)
	setTeamSSOOrg(t, testDB, teamNewer, orgID, true, time.Now().Add(-1*time.Hour))
	setTeamSSOOrg(t, testDB, teamOlder, orgID, true, time.Now().Add(-2*time.Hour))

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      ssoUserProfiles{orgBySubject: map[string]uuid.UUID{subject: orgID}},
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    subject,
		OIDCUserEmail: "ada@example.test",
	}

	team, err := store.bootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("expected sso bootstrap to succeed: %v", err)
	}
	if team.ID != teamOlder {
		t.Fatalf("expected landing team %s (earliest created), got %s", teamOlder, team.ID)
	}

	userIdentity, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	})
	if err != nil {
		t.Fatalf("expected user identity to be created: %v", err)
	}

	// SSO members never get a default team; selection is not pinned.
	if _, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, userIdentity.UserID); err == nil {
		t.Fatal("expected no default team for an SSO member")
	}

	memberships, err := testDB.AuthDB.Read.GetTeamsWithUsersTeams(ctx, userIdentity.UserID)
	if err != nil {
		t.Fatalf("failed to read memberships: %v", err)
	}
	if len(memberships) != 2 {
		t.Fatalf("expected membership in both mapped teams, got %d", len(memberships))
	}
	joined := map[uuid.UUID]bool{}
	for _, m := range memberships {
		if m.IsDefault {
			t.Fatalf("expected no default SSO membership, but %s is default", m.Team.ID)
		}
		joined[m.Team.ID] = true
	}
	if !joined[teamOlder] || !joined[teamNewer] {
		t.Fatalf("expected membership in both %s and %s, got %v", teamOlder, teamNewer, joined)
	}

	if len(sink.requests) != 0 {
		t.Fatalf("expected no billing provisioning for SSO teams, got %d", len(sink.requests))
	}
}

func TestBootstrapOIDCUser_SSOFailsClosedWhenNoTeamMapped(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	orgID := uuid.New()
	subject := uuid.NewString()

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      ssoUserProfiles{orgBySubject: map[string]uuid.UUID{subject: orgID}},
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    subject,
		OIDCUserEmail: "grace@example.test",
	}

	_, err := store.bootstrapOIDCUser(ctx, input)
	if err == nil {
		t.Fatal("expected fail-closed error when organization maps to no team")
	}

	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 ProvisionError, got %v", err)
	}

	// The transaction must roll back: no identity or personal team is left behind.
	if _, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	}); err == nil {
		t.Fatal("expected no user identity after fail-closed bootstrap")
	}

	if len(sink.requests) != 0 {
		t.Fatalf("expected no billing provisioning, got %d", len(sink.requests))
	}
}

func TestBootstrapOIDCUser_SSOSkipsManualTeams(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	orgID := uuid.New()
	subject := uuid.NewString()

	// The org's only team is manual (sso_auto_join = false): it must not be
	// auto-joined, so bootstrap fails closed rather than enrolling the user.
	manualTeam := testutils.CreateTestTeam(t, testDB)
	setTeamSSOOrg(t, testDB, manualTeam, orgID, false, time.Now().Add(-1*time.Hour))

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: sink,
		userProfiles:      ssoUserProfiles{orgBySubject: map[string]uuid.UUID{subject: orgID}},
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    subject,
		OIDCUserEmail: "manual@example.test",
	}

	_, err := store.bootstrapOIDCUser(ctx, input)
	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 fail-closed when the org has only manual teams, got %v", err)
	}
}

func TestCreateTeam_SSOUserRejected(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	userID := uuid.New()

	store := &APIStore{
		userProfiles: ssoUserProfiles{orgByUser: map[uuid.UUID]uuid.UUID{userID: uuid.New()}},
	}

	_, err := store.createTeam(ctx, userID, "My Team")
	if err == nil {
		t.Fatal("expected SSO user to be blocked from creating a team")
	}

	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 ProvisionError, got %v", err)
	}
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

	// invitee belongs to no org (orgByUser empty) → outside the team's org.
	store := &APIStore{userProfiles: ssoUserProfiles{}}
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

	// invitee belongs to the same org as the team → allowed.
	store := &APIStore{
		db:           testDB.SqlcClient,
		authDB:       testDB.AuthDB,
		authService:  noopAuthService{},
		userProfiles: ssoUserProfiles{orgByUser: map[uuid.UUID]uuid.UUID{inviteeID: orgID}},
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

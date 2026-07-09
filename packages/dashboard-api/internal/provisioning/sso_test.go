package provisioning

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

type ssoIdentityProvider struct {
	testIdentityProvider

	orgBySubject map[string]uuid.UUID
	orgByUser    map[uuid.UUID]uuid.UUID
}

func (p ssoIdentityProvider) IdentityOrganizationID(_ context.Context, issuer, subject string) (uuid.UUID, error) {
	return p.orgBySubject[subject], testIssuerRegistered(issuer)
}

func (p ssoIdentityProvider) UserOrganizationID(_ context.Context, userID uuid.UUID) (uuid.UUID, error) {
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

	teamNewer := testutils.CreateTestTeam(t, testDB)
	teamOlder := testutils.CreateTestTeam(t, testDB)
	setTeamSSOOrg(t, testDB, teamNewer, orgID, true, time.Now().Add(-1*time.Hour))
	setTeamSSOOrg(t, testDB, teamOlder, orgID, true, time.Now().Add(-2*time.Hour))

	provider := ssoIdentityProvider{orgBySubject: map[string]uuid.UUID{subject: orgID}}
	svc := New(testDB.AuthDB, provider, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    subject,
		OIDCUserEmail: "ada@example.test",
	}

	team, err := svc.BootstrapOIDCUser(ctx, input)
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

	provider := ssoIdentityProvider{orgBySubject: map[string]uuid.UUID{subject: orgID}}
	svc := New(testDB.AuthDB, provider, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    subject,
		OIDCUserEmail: "grace@example.test",
	}

	_, err := svc.BootstrapOIDCUser(ctx, input)
	if err == nil {
		t.Fatal("expected fail-closed error when organization maps to no team")
	}

	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 ProvisionError, got %v", err)
	}

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

	manualTeam := testutils.CreateTestTeam(t, testDB)
	setTeamSSOOrg(t, testDB, manualTeam, orgID, false, time.Now().Add(-1*time.Hour))

	provider := ssoIdentityProvider{orgBySubject: map[string]uuid.UUID{subject: orgID}}
	svc := New(testDB.AuthDB, provider, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    subject,
		OIDCUserEmail: "manual@example.test",
	}

	_, err := svc.BootstrapOIDCUser(ctx, input)
	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 fail-closed when the org has only manual teams, got %v", err)
	}
}

func TestCreateTeam_SSOUserRejected(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	userID := uuid.New()

	provider := ssoIdentityProvider{orgByUser: map[uuid.UUID]uuid.UUID{userID: uuid.New()}}
	svc := New(nil, provider, &fakeTeamProvisionSink{})

	_, err := svc.CreateTeam(ctx, userID, "My Team")
	if err == nil {
		t.Fatal("expected SSO user to be blocked from creating a team")
	}

	var provErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provErr) || provErr.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 ProvisionError, got %v", err)
	}
}

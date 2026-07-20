package provisioning

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func TestBootstrapAuthProviderUser_CreatesIdentityAndDefaultTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	svc := New(testDB.AuthDB, testIdentityProvider{}, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:      testIssuer,
		OIDCUserID:      uuid.NewString(),
		OIDCUserEmail:   "ada@example.test",
		OIDCUserName:    nil,
		SignupIP:        "198.51.100.20",
		SignupUserAgent: "Mozilla/5.0",
	}

	team, err := svc.BootstrapOIDCUser(ctx, input)
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

type recordingIdentityProvider struct {
	testIdentityProvider

	externalIDSubject string
	externalID        uuid.UUID
	externalIDCalls   int
}

func (r *recordingIdentityProvider) SetIdentityExternalID(_ context.Context, issuer, subject string, externalID uuid.UUID) error {
	r.externalIDSubject = subject
	r.externalID = externalID
	r.externalIDCalls++

	return testIssuerRegistered(issuer)
}

func TestBootstrapOIDCUser_PopulatesOryExternalID(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}
	profiles := &recordingIdentityProvider{}

	svc := New(testDB.AuthDB, profiles, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
	}

	if _, err := svc.BootstrapOIDCUser(ctx, input); err != nil {
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

func TestBootstrapOIDCUser_RoutesConfiguredSecondaryIssuer(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}
	profiles := &recordingIdentityProvider{}
	svc := New(testDB.AuthDB, profiles, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    secondTestIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
	}

	if _, err := svc.BootstrapOIDCUser(ctx, input); err != nil {
		t.Fatalf("expected bootstrap to succeed: %v", err)
	}

	if _, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: secondTestIssuer,
		OidcSub: input.OIDCUserID,
	}); err != nil {
		t.Fatalf("expected secondary issuer identity to be created: %v", err)
	}
	if profiles.externalIDSubject != input.OIDCUserID {
		t.Fatalf("expected external id set on secondary issuer subject %q, got %q", input.OIDCUserID, profiles.externalIDSubject)
	}
}

type failingIdentityProvider struct {
	testIdentityProvider

	externalIDCalls int
}

func (f *failingIdentityProvider) SetIdentityExternalID(_ context.Context, issuer, _ string, _ uuid.UUID) error {
	f.externalIDCalls++
	if err := testIssuerRegistered(issuer); err != nil {
		return err
	}

	return errors.New("ory unavailable")
}

func TestBootstrapOIDCUser_ExternalIDFailureKeepsUserProvisioned(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}
	profiles := &failingIdentityProvider{}

	svc := New(testDB.AuthDB, profiles, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
	}

	team, err := svc.BootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("expected bootstrap to succeed despite external_id failure: %v", err)
	}
	if profiles.externalIDCalls != 1 {
		t.Fatalf("expected one external id attempt, got %d", profiles.externalIDCalls)
	}

	if team.ID == (uuid.UUID{}) {
		t.Fatal("expected provisioned team")
	}

	userIdentity, err := testDB.AuthDB.Read.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: input.OIDCIssuer,
		OidcSub: input.OIDCUserID,
	})
	if err != nil {
		t.Fatalf("expected user identity to be created: %v", err)
	}
	if _, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, userIdentity.UserID); err != nil {
		t.Fatalf("expected default team to be created: %v", err)
	}

	if len(sink.requests) != 1 {
		t.Fatalf("expected one billing provisioning call despite external_id failure, got %d", len(sink.requests))
	}
}

func TestBootstrapOIDCUser_ReRunBackfillsExternalID(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	svc := New(testDB.AuthDB, &failingIdentityProvider{}, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
	}

	// First bootstrap succeeds but logs a warning — external_id is not set yet.
	if _, err := svc.BootstrapOIDCUser(ctx, input); err != nil {
		t.Fatalf("expected first bootstrap to succeed: %v", err)
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

	recording := &recordingIdentityProvider{}
	svc = New(testDB.AuthDB, recording, sink)

	secondTeam, err := svc.BootstrapOIDCUser(ctx, input)
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

	svc := New(testDB.AuthDB, testIdentityProvider{}, sink)

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
		OIDCUserName:  nil,
	}

	const concurrency = 4
	var wg sync.WaitGroup
	results := make(chan ProvisionedTeam, concurrency)
	errs := make(chan error, concurrency)

	for range concurrency {
		wg.Go(func() {
			team, err := svc.BootstrapOIDCUser(ctx, input)
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

func TestBootstrapOIDCUser_AcceptsConfiguredIssuer(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	svc := New(testDB.AuthDB, testIdentityProvider{}, sink)

	team, err := svc.BootstrapOIDCUser(ctx, OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: "ada@example.test",
		OIDCUserName:  nil,
	})
	if err != nil {
		t.Fatalf("expected bootstrap to succeed with the configured Ory issuer: %v", err)
	}
	if team.ID == uuid.Nil {
		t.Fatal("expected provisioned team")
	}
}

func TestBootstrapOIDCUser_UnknownIssuerReturnsBadRequest(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	sink := &fakeTeamProvisionSink{}

	svc := New(testDB.AuthDB, testIdentityProvider{}, sink)

	_, err := svc.BootstrapOIDCUser(ctx, OIDCUserBootstrapInput{
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

// TestBootstrapOIDCUser_PreOryUserEmailMatchReusesExistingAccount verifies that
// a pre-Ory user (in DB with no user_identities row) logs in via Ory for the
// first time and bootstrap reuses their existing user_id and team.
func TestBootstrapOIDCUser_PreOryUserEmailMatchReusesExistingAccount(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	existingUserID := createTestUser(t, testDB)
	existingEmail := testUserEmail(existingUserID)

	existingTeam, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, existingUserID)
	if err != nil {
		t.Fatalf("expected pre-Ory user to have a default team: %v", err)
	}

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	result, err := svc.BootstrapOIDCUser(ctx, OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: existingEmail,
	})
	if err != nil {
		t.Fatalf("expected bootstrap to succeed: %v", err)
	}
	if result.ID != existingTeam.ID {
		t.Fatalf("expected existing team %s to be reused, got %s — pre-Ory user got a new empty account", existingTeam.ID, result.ID)
	}
	if result.UserID != existingUserID {
		t.Fatalf("expected existing user_id %s, got %s", existingUserID, result.UserID)
	}
}

// TestBootstrapOIDCUser_EmailMatchBlockedIfUserAlreadyLinked verifies that a
// user who already has a user_identities row cannot be merged with another
// subject that shares the same email, preventing account takeover.
func TestBootstrapOIDCUser_EmailMatchBlockedIfUserAlreadyLinked(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	existingUserID := createTestUser(t, testDB)

	if err := testDB.AuthDB.TestsRawSQL(ctx,
		`INSERT INTO public.user_identities (oidc_iss, oidc_sub, user_id) VALUES ($1, $2, $3)`,
		testIssuer, uuid.NewString(), existingUserID,
	); err != nil {
		t.Fatalf("failed to insert pre-existing user_identities row: %v", err)
	}

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	result, err := svc.BootstrapOIDCUser(ctx, OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    uuid.NewString(),
		OIDCUserEmail: testUserEmail(existingUserID),
	})
	if err != nil {
		t.Fatalf("expected bootstrap to succeed (creating a new account): %v", err)
	}
	if result.UserID == existingUserID {
		t.Fatal("email-match merged into an already-linked account — account hijack vulnerability")
	}
}

// TestBootstrapOIDCUser_PreOryUserSecondLoginUsesIdentityLookup verifies that
// after the email-match bootstrap, a subsequent login with the same sub resolves
// via the normal identity-lookup path without triggering a second email-match scan.
func TestBootstrapOIDCUser_PreOryUserSecondLoginUsesIdentityLookup(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	existingUserID := createTestUser(t, testDB)
	oidcSub := uuid.NewString()

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	input := OIDCUserBootstrapInput{
		OIDCIssuer:    testIssuer,
		OIDCUserID:    oidcSub,
		OIDCUserEmail: testUserEmail(existingUserID),
	}

	first, err := svc.BootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("first bootstrap failed: %v", err)
	}
	second, err := svc.BootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("second bootstrap failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second login returned different team: first=%s second=%s", first.ID, second.ID)
	}
}

// TestBootstrapOIDCUser_ConcurrentEmailMatchOnlyFirstSubMerges verifies that
// concurrent bootstraps with different subjects but the same email only merge
// one of them into the pre-Ory account.
func TestBootstrapOIDCUser_ConcurrentEmailMatchOnlyFirstSubMerges(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	existingUserID := createTestUser(t, testDB)

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	const concurrency = 4
	type result struct {
		team ProvisionedTeam
		err  error
	}

	results := make(chan result, concurrency)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			team, err := svc.BootstrapOIDCUser(ctx, OIDCUserBootstrapInput{
				OIDCIssuer:    testIssuer,
				OIDCUserID:    uuid.NewString(),
				OIDCUserEmail: testUserEmail(existingUserID),
			})
			results <- result{team, err}
		})
	}
	wg.Wait()
	close(results)

	var mergedCount int
	for r := range results {
		if r.err != nil {
			t.Fatalf("expected bootstrap to succeed: %v", r.err)
		}
		if r.team.UserID == existingUserID {
			mergedCount++
		}
	}

	if mergedCount != 1 {
		t.Fatalf("expected exactly 1 bootstrap to merge into existing account, got %d", mergedCount)
	}
}

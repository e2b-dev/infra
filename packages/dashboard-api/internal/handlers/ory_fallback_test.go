package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

// oryAdminUnavailableProfiles simulates a deployment where the Ory admin API is
// unreachable — e.g. Hydra-only or a CDN that blocks /admin/ paths.
type oryAdminUnavailableProfiles struct{}

func (oryAdminUnavailableProfiles) GetProfilesByUserID(_ context.Context, _ []uuid.UUID) (map[uuid.UUID]userprofile.Profile, error) {
	return nil, errors.New("ory admin api unavailable: connection refused")
}

func (oryAdminUnavailableProfiles) FindProfilesByEmail(_ context.Context, _ string) ([]userprofile.Profile, error) {
	return nil, errors.New("ory admin api unavailable: connection refused")
}

func (oryAdminUnavailableProfiles) GetTeamCreatorContext(_ context.Context, _ uuid.UUID) (*teamprovision.CreatorContextV1, error) {
	return nil, nil
}

func (oryAdminUnavailableProfiles) SetIdentityExternalID(_ context.Context, _ string, _ uuid.UUID) error {
	return nil
}

func (oryAdminUnavailableProfiles) PrepareDeleteUser(_ context.Context, _ uuid.UUID) (userprofile.DeleteUserHandle, error) {
	return nil, nil
}

// --- bootstrap email-match fallback (Issue #3223) ---

// A pre-Ory user (exists in DB with email, no user_identities row) logs in via
// Ory for the first time. Bootstrap must reuse their existing user_id and team
// rather than creating a new empty account that loses all their data.
func TestBootstrapOIDCUser_PreOryUserEmailMatchReusesExistingAccount(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	existingUserID := createHandlerTestUser(t, testDB)
	existingEmail := handlerTestUserEmail(existingUserID)

	existingTeam, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, existingUserID)
	if err != nil {
		t.Fatalf("expected pre-Ory user to have a default team: %v", err)
	}

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
		userProfiles:      newHandlerTestUserProfiles(),
	}

	result, err := store.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    uuid.NewString(), // brand-new Ory sub
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

// A user who already has a user_identities row for this issuer must not be
// merged with another Ory sub that shares the same email.
// This guard prevents an email-based account takeover.
func TestBootstrapOIDCUser_EmailMatchBlockedIfUserAlreadyLinked(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	const oryIssuer = "https://ory.example.test"

	existingUserID := createHandlerTestUser(t, testDB)

	// Link the user to Ory with one sub.
	if err := testDB.AuthDB.TestsRawSQL(ctx,
		`INSERT INTO public.user_identities (oidc_iss, oidc_sub, user_id) VALUES ($1, $2, $3)`,
		oryIssuer, uuid.NewString(), existingUserID,
	); err != nil {
		t.Fatalf("failed to insert pre-existing user_identities row: %v", err)
	}

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: oryIssuer},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
		userProfiles:      newHandlerTestUserProfiles(),
	}

	// A different Ory sub presents the same email — must NOT merge.
	result, err := store.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
		OIDCIssuer:    oryIssuer,
		OIDCUserID:    uuid.NewString(), // different sub
		OIDCUserEmail: handlerTestUserEmail(existingUserID),
	})
	if err != nil {
		t.Fatalf("expected bootstrap to succeed (creating a new account): %v", err)
	}
	if result.UserID == existingUserID {
		t.Fatal("email-match merged into an already-linked account — account hijack vulnerability")
	}
}

// After the email-match bootstrap commits user_identities, a subsequent login
// with the same sub resolves via the normal identity-lookup path without
// triggering a second email-match scan.
func TestBootstrapOIDCUser_PreOryUserSecondLoginUsesIdentityLookup(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	existingUserID := createHandlerTestUser(t, testDB)
	oidcSub := uuid.NewString()

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
		userProfiles:      newHandlerTestUserProfiles(),
	}

	input := oidcUserBootstrapInput{
		OIDCIssuer:    "https://ory.example.test",
		OIDCUserID:    oidcSub,
		OIDCUserEmail: handlerTestUserEmail(existingUserID),
	}

	first, err := store.bootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("first bootstrap failed: %v", err)
	}
	second, err := store.bootstrapOIDCUser(ctx, input)
	if err != nil {
		t.Fatalf("second bootstrap failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second login returned different team: first=%s second=%s", first.ID, second.ID)
	}
}

// --- PostAdminUsersBootstrap response includes user_id (Issue #3222 adjacent) ---

func TestPostAdminUsersBootstrap_ResponseIncludesUserID(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/",
		strings.NewReader(`{"oidc_issuer":"https://ory.example.test","oidc_user_id":"`+uuid.NewString()+`","oidc_user_email":"ada@example.test"}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: "https://ory.example.test"},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
		userProfiles:      newHandlerTestUserProfiles(),
	}
	store.PostAdminUsersBootstrap(ginCtx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	body := recorder.Body.String()
	for _, field := range []string{`"id"`, `"slug"`, `"user_id"`} {
		if !strings.Contains(body, field) {
			t.Fatalf("response missing %s field: %s", field, body)
		}
	}
}

// --- GetTeamsTeamIDMembers DB fallback (Issue #3222) ---

// When the Ory admin API is unreachable, member emails must still be returned
// from the default-team email stored in PostgreSQL.
func TestGetTeamsTeamIDMembers_OryUnavailableFallsBackToDBEmail(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	member1 := createHandlerTestUser(t, testDB)
	member2 := createHandlerTestUser(t, testDB)
	insertHandlerTestTeamMember(t, testDB, member1, teamID, false)
	insertHandlerTestTeamMember(t, testDB, member2, teamID, false)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{
		db:           testDB.SqlcClient,
		authDB:       testDB.AuthDB,
		userProfiles: oryAdminUnavailableProfiles{},
	}
	store.GetTeamsTeamIDMembers(ginCtx, teamID)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 with DB fallback, got %d: %s", recorder.Code, recorder.Body.String())
	}

	body := recorder.Body.String()
	if !strings.Contains(body, handlerTestUserEmail(member1)) {
		t.Fatalf("response missing member1 email (DB fallback): %s", body)
	}
	if !strings.Contains(body, handlerTestUserEmail(member2)) {
		t.Fatalf("response missing member2 email (DB fallback): %s", body)
	}
}

// --- PostTeamsTeamIDMembers DB fallback (Issue #3222) ---

// When the Ory admin API is unreachable, adding a member by email must succeed
// by looking up the user from their default-team email in PostgreSQL.
func TestPostTeamsTeamIDMembers_OryUnavailableFallsBackToDBEmailLookup(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	inviteeID := createHandlerTestUser(t, testDB) // has default team with email in DB
	adderID := createHandlerTestUser(t, testDB)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	reqBody := `{"email":"` + handlerTestUserEmail(inviteeID) + `"}`
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	ginCtx.Request = req
	auth.SetUserIDForTest(t, ginCtx, adderID)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{
		db:           testDB.SqlcClient,
		authDB:       testDB.AuthDB,
		authService:  noopAuthService{},
		userProfiles: oryAdminUnavailableProfiles{},
	}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if ginCtx.Writer.Status() != http.StatusCreated {
		t.Fatalf("expected 201 with DB fallback, got %d: %s", ginCtx.Writer.Status(), recorder.Body.String())
	}
}

// When the Ory admin API is unreachable and the email has no matching user in
// PostgreSQL, the response must be 404.
func TestPostTeamsTeamIDMembers_OryUnavailableUnknownEmailReturns404(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, testDB)
	adderID := createHandlerTestUser(t, testDB)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	reqBody := `{"email":"nobody-` + uuid.NewString() + `@example.test"}`
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	ginCtx.Request = req
	auth.SetUserIDForTest(t, ginCtx, adderID)
	auth.SetTeamInfoForTest(t, ginCtx, &authtypes.Team{
		Team: &authqueries.Team{ID: teamID},
	})

	store := &APIStore{
		db:           testDB.SqlcClient,
		authDB:       testDB.AuthDB,
		userProfiles: oryAdminUnavailableProfiles{},
	}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown email with Ory unavailable, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

// Two concurrent bootstraps with different OIDC subs but the same email must
// not both claim the pre-Ory user_id. The user-row lock taken before the
// identity recheck serializes the race: the first bootstrap merges, the second
// sees the now-populated user_identities row and creates a new account instead.
func TestBootstrapOIDCUser_ConcurrentEmailMatchOnlyFirstSubMerges(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()

	const oryIssuer = "https://ory.example.test"
	existingUserID := createHandlerTestUser(t, testDB)

	store := &APIStore{
		config:            cfg.Config{OryIssuerURL: oryIssuer},
		db:                testDB.SqlcClient,
		authDB:            testDB.AuthDB,
		teamProvisionSink: &fakeTeamProvisionSink{},
		userProfiles:      newHandlerTestUserProfiles(),
	}

	const concurrency = 4
	type result struct {
		team provisionedTeam
		err  error
	}

	results := make(chan result, concurrency)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			team, err := store.bootstrapOIDCUser(ctx, oidcUserBootstrapInput{
				OIDCIssuer:    oryIssuer,
				OIDCUserID:    uuid.NewString(), // each goroutine uses a unique sub
				OIDCUserEmail: handlerTestUserEmail(existingUserID),
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

	// Only one bootstrap must reuse the existing account; the rest create new users.
	if mergedCount != 1 {
		t.Fatalf("expected exactly 1 bootstrap to merge into existing account, got %d", mergedCount)
	}
}

package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/identity"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/provisioning"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

// oryAdminUnavailableIdentity simulates a deployment where the Ory admin API is
// unreachable — e.g. Hydra-only or a CDN that blocks /admin/ paths.
// ProfilesByUserID and FindProfilesByEmail return errors to trigger the DB fallback paths.
// Other methods succeed so that the bootstrap and team-provisioning flows are not blocked.
type oryAdminUnavailableIdentity struct{}

func (oryAdminUnavailableIdentity) ProfilesByUserID(_ context.Context, _ []uuid.UUID) (map[uuid.UUID]identity.Profile, error) {
	return nil, errors.New("ory admin api unavailable: connection refused")
}

func (oryAdminUnavailableIdentity) FindProfilesByEmail(_ context.Context, _ string) ([]identity.Profile, error) {
	return nil, errors.New("ory admin api unavailable: connection refused")
}

func (oryAdminUnavailableIdentity) IdentityOrganizationID(_ context.Context, _, _ string) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (oryAdminUnavailableIdentity) SetIdentityExternalID(_ context.Context, _, _ string, _ uuid.UUID) error {
	return nil
}

func (oryAdminUnavailableIdentity) UserOrganizationID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (oryAdminUnavailableIdentity) TeamCreatorContext(_ context.Context, _ uuid.UUID) (*teamprovision.CreatorContextV1, error) {
	return nil, nil
}

func (oryAdminUnavailableIdentity) PrepareDeleteUser(_ context.Context, _ uuid.UUID) (identity.DeleteUserHandle, error) {
	return nil, nil
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

	idSvc := handlerTestIdentityProvider{}
	store := &APIStore{
		db:                  testDB.SqlcClient,
		authDB:              testDB.AuthDB,
		identityService:     idSvc,
		provisioningService: provisioning.New(testDB.AuthDB, idSvc, &fakeTeamProvisionSink{}),
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
		db:              testDB.SqlcClient,
		authDB:          testDB.AuthDB,
		identityService: oryAdminUnavailableIdentity{},
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
	inviteeID := createHandlerTestUser(t, testDB)
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
		db:              testDB.SqlcClient,
		authDB:          testDB.AuthDB,
		authService:     noopAuthService{},
		identityService: oryAdminUnavailableIdentity{},
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
		db:              testDB.SqlcClient,
		authDB:          testDB.AuthDB,
		identityService: oryAdminUnavailableIdentity{},
	}
	store.PostTeamsTeamIDMembers(ginCtx, teamID)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown email with Ory unavailable, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

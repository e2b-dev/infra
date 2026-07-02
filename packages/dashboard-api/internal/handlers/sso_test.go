package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/provisioning"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// ssoIdentityProvider is a Provider whose organization lookups are configurable, so
// tests can simulate identities that belong to an Ory organization.
type ssoIdentityProvider struct {
	handlerTestIdentityProvider

	orgByUser map[uuid.UUID]uuid.UUID
}

func (p ssoIdentityProvider) GetUserOrganizationID(_ context.Context, userID uuid.UUID) (uuid.UUID, error) {
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

	// invitee belongs to no org (orgByUser empty) → outside the team's org.
	store := &APIStore{
		idp:          ssoIdentityProvider{},
		provisioning: provisioning.New(nil, ssoIdentityProvider{}, nil, ""),
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

	// invitee belongs to the same org as the team → allowed.
	profiles := ssoIdentityProvider{orgByUser: map[uuid.UUID]uuid.UUID{inviteeID: orgID}}
	store := &APIStore{
		db:           testDB.SqlcClient,
		authDB:       testDB.AuthDB,
		authService:  noopAuthService{},
		idp:          profiles,
		provisioning: provisioning.New(testDB.AuthDB, profiles, nil, ""),
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

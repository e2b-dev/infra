package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func runBlockedTeamMiddleware(t *testing.T, method, routePattern, requestPath string, team *authtypes.Team) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	handlerRan := false
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if team != nil {
			auth.SetTeamInfoForTest(t, c, team)
		}
	})
	router.Use(EnforceBlockedTeam)
	router.Handle(method, routePattern, func(c *gin.Context) {
		handlerRan = true
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(method, requestPath, nil)
	router.ServeHTTP(recorder, request)

	return recorder, handlerRan
}

func dashboardBlockedTeam(reason string) *authtypes.Team {
	return &authtypes.Team{
		Team: &authqueries.Team{
			ID:            uuid.New(),
			IsBlocked:     true,
			BlockedReason: &reason,
		},
	}
}

func TestEnforceBlockedTeam_BlockedAllowlistedRead(t *testing.T) {
	t.Parallel()

	w, handlerRan := runBlockedTeamMiddleware(t, http.MethodGet, "/teams/:teamID/members", "/teams/5d363d25-0f74-4a4e-9419-bf420af32fa4/members", dashboardBlockedTeam("missing payment method"))

	if !handlerRan {
		t.Fatal("expected handler to run")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", w.Code)
	}
}

func TestEnforceBlockedTeam_BlockedDeniedMutation(t *testing.T) {
	t.Parallel()

	w, handlerRan := runBlockedTeamMiddleware(t, http.MethodPost, "/teams/:teamID/members", "/teams/5d363d25-0f74-4a4e-9419-bf420af32fa4/members", dashboardBlockedTeam("missing payment method"))

	if handlerRan {
		t.Fatal("expected handler not to run")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestEnforceBlockedTeam_NoTeamOnContext(t *testing.T) {
	t.Parallel()

	w, handlerRan := runBlockedTeamMiddleware(t, http.MethodPost, "/teams", "/teams", nil)

	if !handlerRan {
		t.Fatal("expected handler to run")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", w.Code)
	}
}

func TestEnforceBlockedTeam_UnblockedTeam(t *testing.T) {
	t.Parallel()

	team := &authtypes.Team{
		Team: &authqueries.Team{
			ID: uuid.New(),
		},
	}
	w, handlerRan := runBlockedTeamMiddleware(t, http.MethodPost, "/teams/:teamID/members", "/teams/5d363d25-0f74-4a4e-9419-bf420af32fa4/members", team)

	if !handlerRan {
		t.Fatal("expected handler to run")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", w.Code)
	}
}

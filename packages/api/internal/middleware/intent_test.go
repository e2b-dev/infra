package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func newTestRouter(intents RouteIntents, team *types.Team) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if team != nil {
		r.Use(func(c *gin.Context) {
			auth.SetTeamInfo(c, team)
			c.Next()
		})
	}
	r.Use(AuthorizeTeamAccess(intents))
	r.GET("/sandboxes", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/sandboxes", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.DELETE("/sandboxes/:sandboxID", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.PATCH("/templates/:templateID", func(c *gin.Context) {
		// Simulate a late-team handler: middleware has stashed intent;
		// the handler resolves a team and calls AuthorizeTeamCtx.
		intent, ok := auth.GetIntent(c)
		if !ok {
			c.Status(http.StatusInternalServerError)

			return
		}
		c.JSON(http.StatusOK, gin.H{"intent": string(intent)})
	})
	r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	return r
}

func testIntents() RouteIntents {
	return RouteIntents{
		http.MethodGet: {
			"/sandboxes": auth.IntentView,
		},
		http.MethodPost: {
			"/sandboxes": auth.IntentCreate,
		},
		http.MethodDelete: {
			"/sandboxes/:sandboxID": auth.IntentDelete,
		},
		http.MethodPatch: {
			"/templates/:templateID": auth.IntentMutate,
		},
	}
}

func blockedTeam(reason string) *types.Team {
	r := reason
	return types.NewTeam(&authqueries.Team{IsBlocked: true, BlockedReason: &r}, &authqueries.TeamLimit{})
}

func cleanTeam() *types.Team {
	return types.NewTeam(&authqueries.Team{}, &authqueries.TeamLimit{})
}

func do(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w
}

func TestEnforceIntent_NonBlockedTeamAllowed(t *testing.T) {
	t.Parallel()

	r := newTestRouter(testIntents(), cleanTeam())

	for _, c := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/sandboxes"},
		{http.MethodPost, "/sandboxes"},
		{http.MethodDelete, "/sandboxes/abc"},
	} {
		w := do(t, r, c.method, c.path)
		assert.Equal(t, http.StatusOK, w.Code, "%s %s", c.method, c.path)
	}
}

func TestEnforceIntent_BlockedTeamPolicy(t *testing.T) {
	t.Parallel()

	r := newTestRouter(testIntents(), blockedTeam("verification required"))

	// View / Delete pass.
	w := do(t, r, http.MethodGet, "/sandboxes")
	assert.Equal(t, http.StatusOK, w.Code)

	w = do(t, r, http.MethodDelete, "/sandboxes/abc")
	assert.Equal(t, http.StatusOK, w.Code)

	// Create denied; BlockedReason surfaced in body.
	w = do(t, r, http.MethodPost, "/sandboxes")
	assert.Equal(t, http.StatusForbidden, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["message"].(string), "verification required")
}

func TestEnforceIntent_NoTeamStashesIntent(t *testing.T) {
	t.Parallel()

	// Route registered with no team-injecting middleware — simulates
	// AccessTokenAuth: the handler should be able to read the stashed
	// intent and complete its own enforcement.
	r := newTestRouter(testIntents(), nil)

	w := do(t, r, http.MethodPatch, "/templates/abc")
	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "mutate", body["intent"])
}

func TestEnforceIntent_UnregisteredRouteFailsClosed(t *testing.T) {
	t.Parallel()

	r := newTestRouter(RouteIntents{}, cleanTeam())

	w := do(t, r, http.MethodGet, "/sandboxes")
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["message"].(string), "no action intent declared")
}

func TestEnforceIntent_HealthExempt(t *testing.T) {
	t.Parallel()

	// /health is in IntentExemptRoutes; it must pass even with an empty
	// registry and no team.
	r := newTestRouter(RouteIntents{}, nil)
	w := do(t, r, http.MethodGet, "/health")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnforceIntent_BannedTeamDeniedEvenForView(t *testing.T) {
	t.Parallel()

	banned := types.NewTeam(&authqueries.Team{IsBanned: true}, &authqueries.TeamLimit{})
	r := newTestRouter(testIntents(), banned)

	w := do(t, r, http.MethodGet, "/sandboxes")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestValidateAllRoutesIntentsDeclared_HappyPath(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/sandboxes", func(c *gin.Context) {})
	r.POST("/sandboxes", func(c *gin.Context) {})

	intents := RouteIntents{
		http.MethodGet:  {"/sandboxes": auth.IntentView},
		http.MethodPost: {"/sandboxes": auth.IntentCreate},
	}

	assert.NoError(t, ValidateAllRoutesIntentsDeclared(r.Routes(), intents))
}

func TestValidateAllRoutesIntentsDeclared_MissingRoute(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/sandboxes", func(c *gin.Context) {})
	r.DELETE("/sandboxes/:id", func(c *gin.Context) {})

	intents := RouteIntents{
		http.MethodGet: {"/sandboxes": auth.IntentView},
	}

	err := ValidateAllRoutesIntentsDeclared(r.Routes(), intents)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DELETE /sandboxes/:id")
}

func TestValidateAllRoutesIntentsDeclared_ExemptRoutesIgnored(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/health", func(c *gin.Context) {})

	assert.NoError(t, ValidateAllRoutesIntentsDeclared(r.Routes(), RouteIntents{}))
}

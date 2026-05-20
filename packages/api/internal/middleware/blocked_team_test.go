package middleware

import (
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

// runMiddleware spins up a minimal gin engine that registers a single route
// at fullPath with EnforceBlockedTeam, optionally pre-seeding team info on
// the context. It returns the recorder so callers can assert on status +
// body, plus a boolean indicating whether the dummy handler ran.
func runMiddleware(t *testing.T, method, fullPath string, team *types.Team) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	handlerRan := false

	r := gin.New()
	r.Handle(method, fullPath, func(c *gin.Context) {
		if team != nil {
			auth.SetTeamInfoForTest(t, c, team)
		}
		EnforceBlockedTeam(c)
		if !c.IsAborted() {
			handlerRan = true
			c.Status(http.StatusOK)
		}
	})

	w := httptest.NewRecorder()
	// Use a concrete path that matches fullPath. Replace any :param segments
	// with a stub value — gin still reports the registered pattern via
	// c.FullPath(), which is what the middleware reads.
	req := httptest.NewRequestWithContext(t.Context(), method, concretePath(fullPath), nil)
	r.ServeHTTP(w, req)

	return w, handlerRan
}

// concretePath turns a gin route pattern like "/sandboxes/:sandboxID" into
// a request path "/sandboxes/x" so httptest can hit it.
func concretePath(pattern string) string {
	out := make([]byte, 0, len(pattern))
	i := 0

	for i < len(pattern) {
		if pattern[i] == ':' {
			// Skip past the parameter name (until next '/' or end).
			out = append(out, 'x')
			i++

			for i < len(pattern) && pattern[i] != '/' {
				i++
			}

			continue
		}

		out = append(out, pattern[i])
		i++
	}

	return string(out)
}

func notBlockedTeam() *types.Team {
	return types.NewTeam(
		&authqueries.Team{IsBanned: false, IsBlocked: false},
		&authqueries.TeamLimit{},
	)
}

func blockedTeam(reason string) *types.Team {
	t := &authqueries.Team{IsBlocked: true}
	if reason != "" {
		t.BlockedReason = &reason
	}

	return types.NewTeam(t, &authqueries.TeamLimit{})
}

func TestEnforceBlockedTeam_NoTeamOnContext(t *testing.T) {
	t.Parallel()

	w, handlerRan := runMiddleware(t, http.MethodPost, "/sandboxes", nil)

	assert.True(t, handlerRan, "request should reach handler when no team is on context")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnforceBlockedTeam_NilInnerTeamRow(t *testing.T) {
	t.Parallel()

	w, handlerRan := runMiddleware(t, http.MethodPost, "/sandboxes", &types.Team{})

	assert.True(t, handlerRan, "empty Team struct must be treated as not-blocked")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnforceBlockedTeam_NotBlocked(t *testing.T) {
	t.Parallel()

	w, handlerRan := runMiddleware(t, http.MethodPost, "/sandboxes", notBlockedTeam())

	assert.True(t, handlerRan, "non-blocked team must pass through")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnforceBlockedTeam_BlockedAllowlistedGET(t *testing.T) {
	t.Parallel()

	w, handlerRan := runMiddleware(t, http.MethodGet, "/sandboxes", blockedTeam("test-reason"))

	assert.True(t, handlerRan, "blocked team must reach allowlisted GET")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnforceBlockedTeam_BlockedAllowlistedDELETE(t *testing.T) {
	t.Parallel()

	w, handlerRan := runMiddleware(t, http.MethodDelete, "/sandboxes/:sandboxID", blockedTeam("test-reason"))

	assert.True(t, handlerRan, "blocked team must reach allowlisted DELETE")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEnforceBlockedTeam_BlockedDenied_POST(t *testing.T) {
	t.Parallel()

	w, handlerRan := runMiddleware(t, http.MethodPost, "/sandboxes", blockedTeam("test-reason"))

	require.False(t, handlerRan, "blocked team must NOT reach a non-allowlisted POST")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "team is blocked")
	assert.Contains(t, w.Body.String(), "test-reason")
}

func TestEnforceBlockedTeam_BlockedDenied_MutatingGET(t *testing.T) {
	t.Parallel()

	// GET /templates/:templateID/files/:hash is a *mutating* GET (uploads a
	// layer file). It is intentionally NOT in the allowlist.
	w, handlerRan := runMiddleware(t, http.MethodGet, "/templates/:templateID/files/:hash", blockedTeam("test-reason"))

	require.False(t, handlerRan, "blocked team must NOT reach the mutating GET upload route")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "team is blocked")
}

func TestEnforceBlockedTeam_BlockedDenied_NonAllowlistedDELETE(t *testing.T) {
	t.Parallel()

	// DELETE /templates/tags mutates a live template's tag list — not in
	// the allowlist.
	w, handlerRan := runMiddleware(t, http.MethodDelete, "/templates/tags", blockedTeam("test-reason"))

	require.False(t, handlerRan, "blocked team must NOT reach mutating DELETE /templates/tags")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "team is blocked")
}

func TestEnforceBlockedTeam_BlockedNoReason(t *testing.T) {
	t.Parallel()

	w, handlerRan := runMiddleware(t, http.MethodPost, "/sandboxes", blockedTeam(""))

	require.False(t, handlerRan)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "team is blocked")
}

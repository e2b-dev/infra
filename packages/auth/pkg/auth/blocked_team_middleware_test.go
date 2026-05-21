package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func TestEnforceBlockedTeam(t *testing.T) {
	t.Parallel()

	reason := "payment failed"
	allowlist := BlockedTeamAllowlist{
		http.MethodGet:    {"/sandboxes": {}},
		http.MethodDelete: {"/sandboxes/:sandboxID": {}},
	}

	cases := []struct {
		name           string
		method         string
		fullPath       string
		team           *types.Team
		wantHandlerRan bool
		wantStatus     int
		wantBodyHas    string
	}{
		{
			name:           "no team on context passes",
			method:         http.MethodPost,
			fullPath:       "/sandboxes",
			team:           nil,
			wantHandlerRan: true,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "not blocked passes",
			method:         http.MethodPost,
			fullPath:       "/sandboxes",
			team:           types.NewTeam(&authqueries.Team{IsBlocked: false}, &authqueries.TeamLimit{}),
			wantHandlerRan: true,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "blocked and allowlisted get passes",
			method:         http.MethodGet,
			fullPath:       "/sandboxes",
			team:           types.NewTeam(&authqueries.Team{IsBlocked: true, BlockedReason: &reason}, &authqueries.TeamLimit{}),
			wantHandlerRan: true,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "blocked and allowlisted delete passes",
			method:         http.MethodDelete,
			fullPath:       "/sandboxes/:sandboxID",
			team:           types.NewTeam(&authqueries.Team{IsBlocked: true, BlockedReason: &reason}, &authqueries.TeamLimit{}),
			wantHandlerRan: true,
			wantStatus:     http.StatusOK,
		},
		{
			name:           "blocked and non-allowlisted post denied",
			method:         http.MethodPost,
			fullPath:       "/sandboxes",
			team:           types.NewTeam(&authqueries.Team{IsBlocked: true, BlockedReason: &reason}, &authqueries.TeamLimit{}),
			wantHandlerRan: false,
			wantStatus:     http.StatusForbidden,
			wantBodyHas:    "team is blocked: " + reason,
		},
		{
			name:           "blocked and method mismatch on allowlisted path denied",
			method:         http.MethodPost,
			fullPath:       "/sandboxes/:sandboxID",
			team:           types.NewTeam(&authqueries.Team{IsBlocked: true}, &authqueries.TeamLimit{}),
			wantHandlerRan: false,
			wantStatus:     http.StatusForbidden,
			wantBodyHas:    "team is blocked",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w, ran := runEnforceBlockedTeam(t, allowlist, tc.method, tc.fullPath, tc.team)

			assert.Equal(t, tc.wantHandlerRan, ran)
			assert.Equal(t, tc.wantStatus, w.Code)
			if tc.wantBodyHas != "" {
				assert.Contains(t, w.Body.String(), tc.wantBodyHas)
			}
		})
	}
}

// TestEnforceBlockedTeam_AllowlistParameterization proves the same blocked
// team flips allow vs. deny purely as a function of the allowlist.
func TestEnforceBlockedTeam_AllowlistParameterization(t *testing.T) {
	t.Parallel()

	team := types.NewTeam(&authqueries.Team{IsBlocked: true}, &authqueries.TeamLimit{})
	const method, path = http.MethodGet, "/teams/:teamID/members"

	permissive := BlockedTeamAllowlist{method: {path: {}}}
	restrictive := BlockedTeamAllowlist{}

	wAllowed, ranAllowed := runEnforceBlockedTeam(t, permissive, method, path, team)
	require.True(t, ranAllowed)
	assert.Equal(t, http.StatusOK, wAllowed.Code)

	wDenied, ranDenied := runEnforceBlockedTeam(t, restrictive, method, path, team)
	require.False(t, ranDenied)
	assert.Equal(t, http.StatusForbidden, wDenied.Code)
}

func runEnforceBlockedTeam(
	t *testing.T,
	allowlist BlockedTeamAllowlist,
	method, fullPath string,
	team *types.Team,
) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	handlerRan := false

	r := gin.New()
	r.Handle(method, fullPath, func(c *gin.Context) {
		if team != nil {
			SetTeamInfoForTest(t, c, team)
		}
		EnforceBlockedTeam(allowlist)(c)
		if !c.IsAborted() {
			handlerRan = true
			c.Status(http.StatusOK)
		}
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, concretePath(fullPath), nil)
	r.ServeHTTP(w, req)

	return w, handlerRan
}

// concretePath substitutes ":param" segments in a gin route pattern with
// "x" so httptest can hit the route while c.FullPath() still reports the
// original pattern.
func concretePath(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern))

	i := 0
	for i < len(pattern) {
		if pattern[i] == ':' {
			b.WriteByte('x')
			i++

			for i < len(pattern) && pattern[i] != '/' {
				i++
			}

			continue
		}

		b.WriteByte(pattern[i])
		i++
	}

	return b.String()
}

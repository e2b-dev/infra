package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
)

// blockedTeamAllowlist enumerates gin route patterns (c.FullPath()) that
// blocked teams MAY still reach. Anything not listed here is denied with
// 403 by EnforceBlockedTeam.
//
// Admin (/admin/*, /nodes*) and AccessToken-only (e.g. POST /access-tokens,
// /teams) routes are not listed: they have no team on the gin context, so
// the middleware short-circuits them anyway.
var blockedTeamAllowlist = map[string]map[string]struct{}{
	http.MethodGet: {
		"/api-keys":                                     {},
		"/sandboxes":                                    {},
		"/sandboxes/metrics":                            {},
		"/sandboxes/:sandboxID":                         {},
		"/sandboxes/:sandboxID/logs":                    {},
		"/sandboxes/:sandboxID/metrics":                 {},
		"/snapshots":                                    {},
		"/teams/:teamID/metrics":                        {},
		"/teams/:teamID/metrics/max":                    {},
		"/templates":                                    {},
		"/templates/:templateID":                        {},
		"/templates/:templateID/tags":                   {},
		"/templates/aliases/:alias":                     {},
		"/templates/:templateID/builds/:buildID/logs":   {},
		"/templates/:templateID/builds/:buildID/status": {},
		"/v2/sandboxes":                                 {},
		"/v2/sandboxes/:sandboxID/logs":                 {},
		"/volumes":                                      {},
		"/volumes/:volumeID":                            {},
	},
	http.MethodDelete: {
		"/sandboxes/:sandboxID":         {},
		"/templates/:templateID":        {},
		"/volumes/:volumeID":            {},
		"/api-keys/:apiKeyID":           {},
		"/access-tokens/:accessTokenID": {},
	},
}

// CheckBlockedTeamForRoute returns the underlying *auth.TeamBlockedError if
// the team is blocked AND the matched route is not in blockedTeamAllowlist.
// Returns nil if the team is unblocked, nil, or the route is allowlisted.
//
// Callers that resolve the team late (e.g. APIStore.GetTeam for access-token
// auth) use this directly to mirror what EnforceBlockedTeam does for routes
// where the team is on the gin context at middleware-run-time.
func CheckBlockedTeamForRoute(c *gin.Context, team *types.Team) error {
	err := auth.CheckTeamBlocked(team)
	if err == nil {
		return nil
	}

	if _, allowed := blockedTeamAllowlist[c.Request.Method][c.FullPath()]; allowed {
		return nil
	}

	return err
}

// EnforceBlockedTeam denies requests from blocked teams unless the matched
// route is explicitly allowlisted above. It runs after authentication, so
// banned teams are already rejected upstream by auth.CheckTeamBanned.
//
// No-ops in three cases:
//   - no team on gin context (admin / access-token / pre-auth routes)
//   - team is not blocked
//   - route + method is in blockedTeamAllowlist
func EnforceBlockedTeam(c *gin.Context) {
	team, ok := auth.GetTeamInfo(c)
	if !ok || team == nil {
		c.Next()

		return
	}

	if err := CheckBlockedTeamForRoute(c, team); err != nil {
		apierrors.SendAPIStoreError(c, http.StatusForbidden, err.Error())
		c.Abort()

		return
	}

	c.Next()
}

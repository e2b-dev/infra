package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
)

// blockedTeamAllowlist enumerates gin route patterns (c.FullPath()) that
// blocked teams MAY still reach. Anything not listed here is denied with
// 403 by EnforceBlockedTeam.
//
// User-only and admin routes are not listed: they have no team on the gin
// context, so the middleware short-circuits them.
var blockedTeamAllowlist = map[string]map[string]struct{}{
	http.MethodGet: {
		"/builds":                      {},
		"/builds/statuses":             {},
		"/builds/:build_id":            {},
		"/sandboxes/:sandboxID/record": {},
		"/teams/:teamID/members":       {},
		"/teams/resolve":               {},
		"/templates/defaults":          {},
	},
}

// EnforceBlockedTeam denies requests from blocked teams unless the matched
// route is explicitly allowlisted above. It runs after authentication, so
// banned teams are already rejected upstream by auth.CheckTeamBanned.
func EnforceBlockedTeam(c *gin.Context) {
	team, ok := auth.GetTeamInfo(c)
	if !ok || team == nil {
		c.Next()

		return
	}

	err := auth.CheckTeamBlocked(team)
	if err == nil {
		c.Next()

		return
	}

	if _, allowed := blockedTeamAllowlist[c.Request.Method][c.FullPath()]; allowed {
		c.Next()

		return
	}

	apierrors.SendAPIStoreError(c, http.StatusForbidden, err.Error())
	c.Abort()
}

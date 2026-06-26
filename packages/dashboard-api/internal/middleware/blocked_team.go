package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

// blockedTeamAllowlist enumerates routes a blocked team MAY still reach on
// the dashboard-api service. User-only and admin routes are omitted: they
// have no team on the gin context so the middleware short-circuits.
var blockedTeamAllowlist = auth.BlockedTeamAllowlist{
	http.MethodGet: {
		"/builds":                                      {},
		"/builds/statuses":                             {},
		"/builds/:build_id":                            {},
		"/sandboxes/:sandboxID/record":                 {},
		"/teams/:teamID/members":                       {},
		"/teams/resolve":                               {},
		"/templates":                                   {},
		"/templates/defaults":                          {},
		"/templates/:templateID":                       {},
		"/templates/:templateID/tags/count":            {},
		"/templates/:templateID/tags/exists":           {},
		"/templates/:templateID/tags/groups":           {},
		"/templates/:templateID/tags/:tag/assignments": {},
	},
}

// EnforceBlockedTeam returns the gin middleware for the dashboard-api
// service, configured with the dashboard-specific blocked-team allowlist.
func EnforceBlockedTeam() gin.HandlerFunc {
	return auth.EnforceBlockedTeam(blockedTeamAllowlist)
}

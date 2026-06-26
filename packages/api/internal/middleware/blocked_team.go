package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

// blockedTeamAllowlist enumerates routes a blocked team MAY still reach on
// the api service. Admin and access-token-only routes are omitted: they
// have no team on the gin context so the middleware short-circuits.
var blockedTeamAllowlist = auth.BlockedTeamAllowlist{
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
		"/v2/templates":                                 {},
		"/v2/sandboxes/:sandboxID/logs":                 {},
		"/volumes":                                      {},
		"/volumes/:volumeID":                            {},
	},
	http.MethodDelete: {
		"/sandboxes/:sandboxID":  {},
		"/templates/:templateID": {},
		"/templates/tags":        {},
		"/volumes/:volumeID":     {},
		"/api-keys/:apiKeyID":    {},
	},
}

// EnforceBlockedTeam returns the gin middleware for the api service,
// configured with the api-specific blocked-team allowlist.
func EnforceBlockedTeam() gin.HandlerFunc {
	return auth.EnforceBlockedTeam(blockedTeamAllowlist)
}

// CheckTeamAccessForRoute applies CheckTeamBanned + the api blocked-team
// allowlist to a late-resolved team.
func CheckTeamAccessForRoute(c *gin.Context, team *types.Team) error {
	return auth.CheckTeamAccess(c, team, blockedTeamAllowlist)
}

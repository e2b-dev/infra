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

// EnforceBlockedTeam returns the gin middleware for the api service,
// configured with the api-specific blocked-team allowlist.
func EnforceBlockedTeam() gin.HandlerFunc {
	return auth.EnforceBlockedTeam(blockedTeamAllowlist)
}

// CheckBlockedTeamForRoute applies the api allowlist to a late-resolved
// team (access-token / user-id auth), mirroring EnforceBlockedTeam.
func CheckBlockedTeamForRoute(c *gin.Context, team *types.Team) error {
	return auth.CheckBlockedTeamForRoute(c, team, blockedTeamAllowlist)
}

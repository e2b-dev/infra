package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

// RouteIntents maps an HTTP method to a per-route intent table keyed by
// gin's route pattern (the value returned by c.FullPath()).
type RouteIntents map[string]map[string]auth.ActionIntent

// IntentExemptRoutes are gin route patterns that should never have an
// intent declared (e.g. /health). Listed explicitly so a missing entry in
// RouteIntents for an exempt route doesn't trip the startup validator.
var IntentExemptRoutes = map[string]struct{}{
	"/health": {},
}

// AuthorizeTeamAccess reads the matched route, looks up its intent in
// `intents`, and applies the policy via auth.AuthorizeTeam. It must run
// after authentication so the team is available in the gin context for
// routes authenticated by API key or Supabase team token.
//
// For routes authenticated by access token (no team in context), the
// middleware stashes the intent via auth.SetIntent so the handler can
// call auth.AuthorizeTeamCtx after it resolves the team itself.
//
// Returns 403 with the team's BlockedReason when enforcement denies the
// request, 500 when the route is not in the registry (which should never
// happen — the startup validator catches missing routes).
func AuthorizeTeamAccess(intents RouteIntents) gin.HandlerFunc {
	return func(c *gin.Context) {
		route := c.FullPath()
		if _, exempt := IntentExemptRoutes[route]; exempt {
			c.Next()

			return
		}

		intent, ok := intents[c.Request.Method][route]
		if !ok {
			// Should never happen — ValidateAllRoutesIntentsDeclared panics at
			// boot if any registered route is missing from the registry.
			// Fail closed at runtime so a missing entry doesn't silently
			// open a hole.
			abortWith(c, http.StatusInternalServerError, "no action intent declared for route")

			return
		}

		auth.SetIntent(c, intent)

		team, hasTeam := auth.GetTeamInfo(c)
		if !hasTeam || team == nil {
			// Late-team auth (AccessTokenAuth): handler is responsible
			// for calling auth.AuthorizeTeamCtx after team load.
			c.Next()

			return
		}

		if err := auth.AuthorizeTeam(*team, intent); err != nil {
			var blocked *auth.TeamBlockedError
			if errors.As(err, &blocked) {
				abortWith(c, http.StatusForbidden, blocked.Error())

				return
			}

			var forbidden *auth.TeamForbiddenError
			if errors.As(err, &forbidden) {
				abortWith(c, http.StatusForbidden, forbidden.Error())

				return
			}

			abortWith(c, http.StatusForbidden, err.Error())

			return
		}

		c.Next()
	}
}

// ValidateAllRoutesIntentsDeclared walks the gin route tree and verifies every
// registered route either appears in `intents` or is listed in
// IntentExemptRoutes. Returns a stable, sorted error listing the missing
// routes. Call after RegisterHandlersWithOptions and fail fast on error so
// misconfigurations never reach production.
func ValidateAllRoutesIntentsDeclared(routes gin.RoutesInfo, intents RouteIntents) error {
	var missing []string
	for _, r := range routes {
		if _, exempt := IntentExemptRoutes[r.Path]; exempt {
			continue
		}

		if _, ok := intents[r.Method][r.Path]; !ok {
			missing = append(missing, fmt.Sprintf("%s %s", r.Method, r.Path))
		}
	}

	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)

	return fmt.Errorf("routes missing action-intent declaration: %v", missing)
}

func abortWith(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"code":    status,
		"message": message,
	})
}

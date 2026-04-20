package middleware

import (
	"net/http"
	"regexp"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

const disableTeamBlockedExtension = "x-disable-team-blocked"

// BlockedTeamRoutes is a set of gin route patterns keyed by HTTP method that
// must deny access to blocked teams.
type BlockedTeamRoutes map[string]map[string]struct{}

// BuildBlockedTeamRoutes scans the OpenAPI spec and returns the set of
// (method, gin-route) pairs marked with `x-disable-team-blocked: true`.
func BuildBlockedTeamRoutes(swagger *openapi3.T) BlockedTeamRoutes {
	routes := BlockedTeamRoutes{}
	if swagger == nil || swagger.Paths == nil {
		return routes
	}

	for rawPath, pathItem := range swagger.Paths.Map() {
		if pathItem == nil {
			continue
		}
		ginRoute := openAPIPathToGinRoute(rawPath)
		for method, op := range pathItem.Operations() {
			if op == nil {
				continue
			}
			raw, ok := op.Extensions[disableTeamBlockedExtension]
			if !ok {
				continue
			}
			enabled, ok := raw.(bool)
			if !ok || !enabled {
				continue
			}
			if routes[method] == nil {
				routes[method] = map[string]struct{}{}
			}
			routes[method][ginRoute] = struct{}{}
		}
	}

	return routes
}

// contains reports whether the given HTTP method and gin route pattern is
// marked as restricted for blocked teams.
func (r BlockedTeamRoutes) contains(method, route string) bool {
	methodRoutes, ok := r[method]
	if !ok {
		return false
	}
	_, ok = methodRoutes[route]

	return ok
}

// BlockedTeam denies requests from blocked teams for routes marked with
// `x-disable-team-blocked: true`. Must run after authentication so the team
// is available in the gin context.
func BlockedTeam(routes BlockedTeamRoutes) gin.HandlerFunc {
	return func(c *gin.Context) {
		team, ok := auth.GetTeamInfo(c)
		if !ok || team == nil || !team.IsBlocked {
			c.Next()

			return
		}

		if !routes.contains(c.Request.Method, c.FullPath()) {
			c.Next()

			return
		}

		message := "team is blocked"
		if team.BlockedReason != nil && *team.BlockedReason != "" {
			message = *team.BlockedReason
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code":    http.StatusForbidden,
			"message": message,
		})
	}
}

var openAPIParamRegex = regexp.MustCompile(`\{([^{}]+)\}`)

// openAPIPathToGinRoute converts an OpenAPI path like `/sandboxes/{sandboxID}`
// into the gin route pattern `/sandboxes/:sandboxID` that matches c.FullPath().
func openAPIPathToGinRoute(p string) string {
	return openAPIParamRegex.ReplaceAllString(p, ":$1")
}

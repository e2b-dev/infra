package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// The secret management routes carry the customer's secret value in the
// request body and nowhere else. This package marks the path family for the
// two boundaries that key off it — the out-of-engine no-store wrapper below
// and the body-blind validation error handler — and owns the fixed response
// texts they share. Everything else about a secrets request is handled by the
// same shared middleware as any other route.
const (
	// secretsPathPrefix is the path family the middleware acts on.
	secretsPathPrefix = "/secrets"

	cacheControlHeader       = "Cache-Control"
	cacheControlNoStoreValue = "no-store"
)

// The fixed public messages shared by everything that can answer a secrets
// request: this package, the request-validation error handler, and the
// handlers. A caller learns the class of the failure and nothing else.
const (
	SecretsInvalidRequestMessage = "Invalid secrets request"
	SecretsUnauthorizedMessage   = "You are not authenticated"
	SecretsUnavailableMessage    = "Secrets are not available for this team"
	SecretsNotFoundMessage       = "Secret not found"
	SecretsConflictMessage       = "Secret cannot be modified in its current state"
	SecretsBackendMessage        = "Secrets backend error"
	SecretsBackendTimeoutMessage = "Secrets backend timed out"
)

// IsSecretsRoute reports whether a request targets the secret management path
// family. It answers for unmatched paths too, so a 404 below /secrets is
// treated as confidentially as a matched route.
func IsSecretsRoute(c *gin.Context) bool {
	return c.Request != nil && c.Request.URL != nil && isSecretsPath(c.Request.URL.Path)
}

// NoStoreSecrets marks every response on the secrets path family before any
// later layer writes, whichever one answers: auth, validation, the rate
// limiter, the handlers, or a recovered panic. Gin's own canonical-path
// redirects bypass the middleware chain and are deliberately not covered:
// they carry no body and no customer data.
func NoStoreSecrets() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsSecretsRoute(c) {
			c.Writer.Header().Set(cacheControlHeader, cacheControlNoStoreValue)
		}

		c.Next()
	}
}

func isSecretsPath(path string) bool {
	return path == secretsPathPrefix || strings.HasPrefix(path, secretsPathPrefix+"/")
}

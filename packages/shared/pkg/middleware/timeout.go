package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ErrRequestTimeout is the cancel cause set when the per-request timeout fires.
// Callers can distinguish this from a client disconnection by checking:
//
//	errors.Is(context.Cause(ctx), middleware.ErrRequestTimeout)
var ErrRequestTimeout = errors.New("request timeout exceeded")

// StatusClientClosedRequest is the de-facto status code (used by nginx) for a
// client that closed the connection before the server could send a response.
// It is not an official IANA code but is widely recognised in logs and metrics.
const StatusClientClosedRequest = 499

// RequestTimeout returns a Gin middleware that sets a context deadline on each
// request. This is needed because http.Server.WriteTimeout does NOT cancel
// r.Context() (see https://github.com/golang/go/issues/59602), so without an
// explicit deadline, blocking calls like pgxpool.Acquire will wait indefinitely
// when the connection pool is saturated.
//
// After the handler returns, the middleware checks context.Cause to distinguish
// two cancellation scenarios and sets an appropriate status if nothing was
// written yet:
//   - server-side timeout (cause is ErrRequestTimeout)  → 408 Request Timeout
//   - client disconnect (ctx.Err() == context.Canceled) → 499 Client Closed Request
//
// Routes matching any of the excludedRoutes patterns are skipped (useful for
// health checks and long-polling endpoints).
func RequestTimeout(timeout time.Duration, excludedRoutes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if timeoutShouldSkip(c.Request.URL.Path, excludedRoutes) {
			c.Next()

			return
		}

		ctx, cancel := context.WithTimeoutCause(c.Request.Context(), timeout, fmt.Errorf("%w after %s", ErrRequestTimeout, timeout))
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func timeoutShouldSkip(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if timeoutMatchPattern(path, pattern) {
			return true
		}
	}

	return false
}

func timeoutMatchPattern(path, pattern string) bool {
	pathSegments := strings.Split(path, "/")
	patternSegments := strings.Split(pattern, "/")

	if len(pathSegments) != len(patternSegments) {
		return false
	}

	for i := range pathSegments {
		if patternSegments[i] != pathSegments[i] && !strings.HasPrefix(patternSegments[i], ":") {
			return false
		}
	}

	return true
}

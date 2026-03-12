package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestTimeout returns a Gin middleware that sets a context deadline on each
// request. This is needed because http.Server.WriteTimeout does NOT cancel
// r.Context() (see https://github.com/golang/go/issues/59602), so without an
// explicit deadline, blocking calls like pgxpool.Acquire will wait indefinitely
// when the connection pool is saturated.
//
// If the deadline is exceeded and the handler has not yet written a response,
// the middleware responds with 503 Service Unavailable.
//
// Routes matching any of the excludedRoutes patterns are skipped (useful for
// health checks and long-polling endpoints).
func RequestTimeout(timeout time.Duration, excludedRoutes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if shouldSkip(c.Request.URL.Path, excludedRoutes) {
			c.Next()

			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		if ctx.Err() == context.DeadlineExceeded && !c.Writer.Written() {
			c.String(http.StatusServiceUnavailable, "request timed out")
			c.Abort()
		}
	}
}

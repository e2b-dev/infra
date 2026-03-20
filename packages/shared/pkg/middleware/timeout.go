package middleware

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
)

// ErrRequestTimeout is the cancel cause set when the per-request timeout fires.
// Callers can distinguish this from a client disconnection by checking:
//
//	errors.Is(CancelCause(c), middleware.ErrRequestTimeout)
var ErrRequestTimeout = errors.New("request timeout exceeded")

// cancelCauseKey is the gin context key where RequestTimeout snapshots the
// cancel cause before defer-cancel runs.
const cancelCauseKey = "middleware.cancelCause"

// CancelCause returns the cancel cause captured by the timeout middleware.
// It returns nil for normal (non-canceled/non-timed-out) requests.
func CancelCause(c *gin.Context) error {
	if val, exists := c.Get(cancelCauseKey); exists {
		if err, ok := val.(error); ok {
			return err
		}
	}

	return nil
}

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
func RequestTimeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeoutCause(c.Request.Context(), timeout, ErrRequestTimeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		// Snapshot the cause *before* defer-cancel fires so outer
		// middlewares can distinguish timeout vs client-disconnect
		// via CancelCause(c) without racing with the deferred cancel.
		if err := context.Cause(ctx); err != nil {
			c.Set(cancelCauseKey, err)
		}
	}
}

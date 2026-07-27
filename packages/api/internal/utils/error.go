package utils

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	securityErrPrefix      = "error in openapi3filter.SecurityRequirementsError: security requirements failed: "
	forbiddenErrPrefix     = "team forbidden: "
	blockedErrPrefix       = "team blocked: "
	clientDisconnectPrefix = "client disconnected: "
)

func ErrorHandler(c *gin.Context, message string, statusCode int) {
	ctx := c.Request.Context()

	// Client dropped the connection before the request body was fully received.
	// kin-openapi reads the body before calling auth; when the TCP read fails
	// it wraps the net.Error in a SecurityRequirementsError, which would otherwise
	// be logged/traced as an auth failure. This is a client-side network event,
	// not a server error — record informationally and return 499 so metrics
	// distinguish it from real auth failures.
	if after, ok := strings.CutPrefix(message, clientDisconnectPrefix); ok {
		telemetry.ReportEvent(ctx, "client disconnected before request body received",
			attribute.String("disconnect.reason", after),
		)
		c.AbortWithStatus(499)

		return
	}

	var errMsg error

	switch {
	case strings.HasPrefix(c.Request.URL.Path, "/instances"),
		strings.HasPrefix(c.Request.URL.Path, "/envs"):
		errMsg = fmt.Errorf("OpenAPI validation error, old endpoints: %s", message)
		message = "Endpoints are deprecated, please update your SDK to use the new endpoints."
	case strings.HasPrefix(c.Request.URL.Path, "/templates") && strings.HasPrefix(c.Request.Header.Get("Content-Type"), "multipart/form-data"):
		errMsg = fmt.Errorf("OpenAPI validation error, old CLI: %s", message)
		message = "Endpoint deprecated please update your CLI to the latest version"
	default:
		data, err := c.GetRawData()
		if err == nil {
			errMsg = fmt.Errorf("OpenAPI validation error: %s, data: %s", message, data)
		} else {
			errMsg = fmt.Errorf("OpenAPI validation error: %s, body read error: %w", message, err)
		}
	}

	telemetry.ReportError(ctx, message, errMsg, attribute.Int("http.status_code", statusCode))

	c.Error(errMsg)

	// Handle forbidden errors
	if after, ok := strings.CutPrefix(message, forbiddenErrPrefix); ok {
		c.AbortWithStatusJSON(
			http.StatusForbidden,
			gin.H{
				"code":    http.StatusForbidden,
				"message": after,
			},
		)

		return
	}

	// Handle blocked errors
	if after, ok := strings.CutPrefix(message, blockedErrPrefix); ok {
		c.AbortWithStatusJSON(
			http.StatusForbidden,
			gin.H{
				"code":    http.StatusForbidden,
				"message": after,
			},
		)

		return
	}

	// Handle security requirements errors from the openapi3filter
	if after, ok := strings.CutPrefix(message, securityErrPrefix); ok {
		// Keep the original status code as it can be also timeout (read body timeout) error code.
		// The securityErrPrefix is added for all errors going through the processCustomErrors function.
		c.AbortWithStatusJSON(
			statusCode,
			gin.H{
				"code":    statusCode,
				"message": after,
			},
		)

		return
	}

	c.AbortWithStatusJSON(statusCode, gin.H{"code": statusCode, "message": fmt.Errorf("validation error: %s", message).Error()})
}

// MultiErrorHandler handles wrapped SecurityRequirementsError, so there are no multiple errors returned to the user.
func MultiErrorHandler(me openapi3.MultiError) error {
	if len(me) == 0 {
		return nil
	}
	err := me[0]

	// Recreate logic from oapi-codegen/gin-middleware to handle the error
	// Source: https://github.com/oapi-codegen/gin-middleware/blob/main/oapi_validate.go
	switch e := err.(type) { //nolint:errorlint  // we copied this and don't want it to change
	case *openapi3filter.RequestError:
		// We've got a bad request
		// Split up the verbose error by lines and return the first one
		// openapi errors seem to be multi-line with a decent message on the first
		errorLines := strings.Split(e.Error(), "\n")

		return fmt.Errorf("error in openapi3filter.RequestError: %s", errorLines[0])
	case *openapi3filter.SecurityRequirementsError:
		return processCustomErrors(e) // custom implementation
	default:
		// This should never happen today, but if our upstream code changes,
		// we don't want to crash the server, so handle the unexpected error.
		return fmt.Errorf("error validating request: %w", err)
	}
}

func processCustomErrors(e *openapi3filter.SecurityRequirementsError) error {
	// Return only one security requirement error (there may be multiple securitySchemes)
	unwrapped := e.Errors
	err := unwrapped[0]

	var teamForbidden *sharedauth.TeamForbiddenError
	var teamBlocked *sharedauth.TeamBlockedError
	// Return only the first non-missing authorization header error (if possible)
	for _, errW := range unwrapped {
		if errors.Is(errW, sharedauth.ErrNoAuthHeader) {
			continue
		}

		if errors.As(errW, &teamForbidden) {
			return fmt.Errorf("%s%s", forbiddenErrPrefix, teamForbidden.Error())
		}

		if errors.As(errW, &teamBlocked) {
			return fmt.Errorf("%s%s", blockedErrPrefix, teamBlocked.Error())
		}

		// kin-openapi reads the entire request body before invoking auth functions.
		// When that TCP read fails (client timeout / server ReadTimeout), it returns
		// a RequestError{Reason:"reading failed"} wrapped in SecurityRequirementsError.
		// Detect this case so it isn't misclassified as an authentication failure.
		var reqErr *openapi3filter.RequestError
		if errors.As(errW, &reqErr) && reqErr.Reason == "reading failed" {
			var netErr net.Error
			if errors.As(reqErr.Err, &netErr) {
				return fmt.Errorf("%s%s", clientDisconnectPrefix, reqErr.Err.Error())
			}
		}

		err = errW

		break
	}

	return fmt.Errorf("%s%s", securityErrPrefix, err.Error())
}

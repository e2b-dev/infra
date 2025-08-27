package utils

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	securityErrPrefix  = "error in openapi3filter.SecurityRequirementsError: security requirements failed: "
	forbiddenErrPrefix = "team forbidden: "
	blockedErrPrefix   = "team blocked: "
)

func ErrorHandler(c *gin.Context, message string, statusCode int) {
	var errMsg error

	ctx := c.Request.Context()

	if strings.HasPrefix(c.Request.URL.Path, "/instances") ||
		strings.HasPrefix(c.Request.URL.Path, "/envs") {
		errMsg = fmt.Errorf("OpenAPI validation error, old endpoints: %s", message)
		message = "Endpoints are deprecated, please update your SDK to use the new endpoints."
	} else if strings.HasPrefix(c.Request.URL.Path, "/templates") && strings.HasPrefix(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		errMsg = fmt.Errorf("OpenAPI validation error, old CLI: %s", message)
		message = "Endpoint deprecated please update your CLI to the latest version"
	} else {
		data, err := c.GetRawData()
		if err == nil {
			errMsg = fmt.Errorf("OpenAPI validation error: %s, data: %s", message, data)
		} else {
			errMsg = fmt.Errorf("OpenAPI validation error: %s, body read error: %w", message, err)
		}
	}

	telemetry.ReportError(ctx, message, errMsg)

	c.Error(errMsg)

	// Handle forbidden errors
	if strings.HasPrefix(message, forbiddenErrPrefix) {
		c.AbortWithStatusJSON(
			http.StatusForbidden,
			gin.H{
				"code":    http.StatusForbidden,
				"message": strings.TrimPrefix(message, forbiddenErrPrefix),
			},
		)

		return
	}

	// Handle blocked errors
	if strings.HasPrefix(message, blockedErrPrefix) {
		c.AbortWithStatusJSON(
			http.StatusForbidden,
			gin.H{
				"code":    http.StatusForbidden,
				"message": strings.TrimPrefix(message, blockedErrPrefix),
			},
		)

		return
	}

	// Handle security requirements errors from the openapi3filter
	if strings.HasPrefix(message, securityErrPrefix) {
		c.AbortWithStatusJSON(
			http.StatusUnauthorized,
			gin.H{
				"code":    http.StatusUnauthorized,
				"message": strings.TrimPrefix(message, securityErrPrefix),
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
	{
		var e *openapi3filter.RequestError
		var e1 *openapi3filter.SecurityRequirementsError
		switch {
		case errors.As(err, &e):
			errorLines := strings.Split(e.Error(), "\n")
			return fmt.Errorf("error in openapi3filter.RequestError: %s", errorLines[0])
		case errors.As(err, &e1):
			unwrapped := e1.Errors
			err = unwrapped[0]
			var teamForbidden *db.TeamForbiddenError
			var teamBlocked *db.TeamBlockedError
			for _, errW := range unwrapped {
				if errors.Is(errW, auth.ErrNoAuthHeader) {
					continue
				}
				if errors.As(errW, &teamForbidden) {
					return fmt.Errorf("%s%s", forbiddenErrPrefix, err.Error())
				}
				if errors.As(errW, &teamBlocked) {
					return fmt.Errorf("%s%s", blockedErrPrefix, err.Error())
				}
				err = errW
				break
			}
			return fmt.Errorf("%s%s", securityErrPrefix, err.Error())
		default:
			return fmt.Errorf("error validating request: %w", err)
		}
	}
}

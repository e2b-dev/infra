package utils

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func ErrorHandler(c *gin.Context, message string, statusCode int) {
	var errMsg error

	ctx := c.Request.Context()

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
	if after, ok := strings.CutPrefix(message, auth.ForbiddenErrPrefix); ok {
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
	if after, ok := strings.CutPrefix(message, auth.BlockedErrPrefix); ok {
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
	if after, ok := strings.CutPrefix(message, auth.SecurityErrPrefix); ok {
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

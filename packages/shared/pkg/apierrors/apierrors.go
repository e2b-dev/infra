package apierrors

import (
	"errors"

	"github.com/gin-gonic/gin"
)

var _ error = (*APIError)(nil)

// APIError represents a structured error with an HTTP status code and client-facing message.
type APIError struct {
	Err       error
	ClientMsg string
	Code      int
	// ErrorCode is an optional machine-readable semantic code (e.g.
	// "sandbox_capacity_unavailable") rendered as error_code in the body.
	ErrorCode string
}

func (e *APIError) Error() string {
	return e.Err.Error()
}

// SendAPIStoreError sends a JSON error response and records the error on the gin context.
func SendAPIStoreError(c *gin.Context, code int, message string) {
	SendAPIError(c, &APIError{Code: code, ClientMsg: message})
}

// SendAPIError sends a JSON error response like SendAPIStoreError, adding
// error_code to the body when the error carries a semantic code.
func SendAPIError(c *gin.Context, apiErr *APIError) {
	c.Error(errors.New(apiErr.ClientMsg))

	body := gin.H{
		"code":    int32(apiErr.Code),
		"message": apiErr.ClientMsg,
	}
	if apiErr.ErrorCode != "" {
		body["error_code"] = apiErr.ErrorCode
	}

	c.JSON(apiErr.Code, body)
}

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
}

func (e *APIError) Error() string {
	return e.Err.Error()
}

// SendAPIStoreError sends a JSON error response and records the error on the gin context.
func SendAPIStoreError(c *gin.Context, code int, message string) {
	c.Error(errors.New(message))
	c.JSON(code, gin.H{
		"code":    int32(code),
		"message": message,
	})
}

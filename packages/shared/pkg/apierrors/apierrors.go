package apierrors

import (
	"errors"

	"github.com/gin-gonic/gin"
)

var _ error = (*APIError)(nil)

// Stable, machine-readable error codes. SDKs switch on these; humans read
// the accompanying `message`. Keep the set small and deliberate — this is
// wire protocol.
const (
	// ErrCodeConcurrencyLimitExceeded: a hard concurrency quota (e.g. the
	// maximum number of live sandboxes or in-flight template builds) was
	// reached. Releasing an existing resource is what unblocks further
	// requests — waiting alone does not.
	ErrCodeConcurrencyLimitExceeded = "concurrency_limit_exceeded"
)

// APIError represents a structured error with an HTTP status code and
// client-facing message.
//
// ErrorCode is optional; when set, it's emitted alongside message so SDKs
// can programmatically distinguish failure modes from the generic
// `{code, message}` body.
type APIError struct {
	Err       error
	ClientMsg string
	Code      int
	ErrorCode string
}

func (e *APIError) Error() string {
	return e.Err.Error()
}

// SendAPIStoreError sends a JSON error response and records the error on
// the gin context.
func SendAPIStoreError(c *gin.Context, code int, message string) {
	SendAPIStoreErrorWithCode(c, code, "", message)
}

// SendAPIStoreErrorWithCode is SendAPIStoreError with an explicit structured
// error code. An empty errorCode is omitted from the response body to keep
// backward compatibility with the pre-existing `{code, message}` shape.
func SendAPIStoreErrorWithCode(c *gin.Context, code int, errorCode, message string) {
	c.Error(errors.New(message))

	body := gin.H{
		"code":    int32(code),
		"message": message,
	}
	if errorCode != "" {
		body["errorCode"] = errorCode
	}
	c.JSON(code, body)
}

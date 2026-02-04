package templatecache

import (
	"errors"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

var (
	ErrTemplateNotFound = errors.New("template not found")
	ErrAccessDenied     = errors.New("access denied")
	ErrClusterMismatch  = errors.New("template not available in cluster")
)

// ErrorToAPIError maps template cache errors to API errors.
// The identifier parameter is used to provide context in error messages.
func ErrorToAPIError(err error, identifier string) *api.APIError {
	switch {
	case errors.Is(err, ErrTemplateNotFound):
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: "template '" + identifier + "' not found",
			Err:       err,
		}
	case errors.Is(err, ErrAccessDenied):
		return &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: "you don't have access to template '" + identifier + "'",
			Err:       err,
		}
	case errors.Is(err, ErrClusterMismatch):
		return &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: "template '" + identifier + "' is not available in the requested cluster",
			Err:       err,
		}
	default:
		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "error resolving template '" + identifier + "'",
			Err:       err,
		}
	}
}

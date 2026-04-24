package templatecache

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

var (
	ErrTemplateNotFound    = errors.New("template not found")
	ErrTemplateTagNotFound = errors.New("template tag not found")
	ErrAccessDenied        = errors.New("access denied")
	ErrClusterMismatch     = errors.New("template not available in cluster")
)

func ErrorToAPIError(err error, identifier string) *api.APIError {
	return ErrorToAPIErrorWithTemplate(err, identifier, "", nil)
}

func ErrorToAPIErrorWithTemplate(err error, identifier, templateID string, tag *string) *api.APIError {
	clientMsg := lookupTemplateErrorMessage(err, identifier, templateID, tag)

	switch {
	case errors.Is(err, ErrTemplateTagNotFound):
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: clientMsg,
			Err:       err,
		}
	case errors.Is(err, ErrTemplateNotFound):
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: clientMsg,
			Err:       err,
		}
	case errors.Is(err, ErrAccessDenied):
		return &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: clientMsg,
			Err:       err,
		}
	case errors.Is(err, ErrClusterMismatch):
		return &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: clientMsg,
			Err:       err,
		}
	default:
		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: clientMsg,
			Err:       err,
		}
	}
}

func lookupTemplateErrorMessage(err error, identifier, templateID string, tag *string) string {
	label := formatTemplateRef(identifier, templateID)

	switch {
	case errors.Is(err, ErrTemplateTagNotFound):
		if tag != nil {
			return fmt.Sprintf("template %s with tag '%s' not found", label, *tag)
		}

		return fmt.Sprintf("template %s has no ready build", label)
	case errors.Is(err, ErrTemplateNotFound):
		return fmt.Sprintf("template %s not found", label)
	case errors.Is(err, ErrAccessDenied):
		return fmt.Sprintf("you don't have access to template %s", label)
	case errors.Is(err, ErrClusterMismatch):
		return fmt.Sprintf("template %s is not available in the requested cluster", label)
	default:
		return fmt.Sprintf("error resolving template %s", label)
	}
}

func formatTemplateRef(identifier, templateID string) string {
	if templateID == "" || templateID == identifier {
		return "'" + identifier + "'"
	}

	return fmt.Sprintf("'%s' (%s)", identifier, templateID)
}

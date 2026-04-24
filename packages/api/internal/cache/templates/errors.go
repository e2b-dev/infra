package templatecache

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

type undisclosedTemplateNotFoundError struct{}

func (undisclosedTemplateNotFoundError) Error() string {
	return ErrTemplateNotFound.Error()
}

func (undisclosedTemplateNotFoundError) Is(target error) bool {
	return target == ErrTemplateNotFound
}

var (
	ErrTemplateNotFound            = errors.New("template not found")
	ErrTemplateNotFoundUndisclosed = undisclosedTemplateNotFoundError{}
	ErrTemplateTagNotFound         = errors.New("template tag not found")
	ErrAccessDenied                = errors.New("access denied")
	ErrClusterMismatch             = errors.New("template not available in cluster")
)

func ErrorToAPIError(err error, identifier string) *api.APIError {
	return ErrorToAPIErrorWithTemplate(err, identifier, "", nil)
}

func ErrorToAPIErrorWithTemplate(err error, identifier, templateID string, tag *string) *api.APIError {
	label := formatTemplateRef(identifier, templateID)

	switch {
	case errors.Is(err, ErrTemplateNotFoundUndisclosed):
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: fmt.Sprintf("template '%s' not found", identifier),
			Err:       err,
		}
	case errors.Is(err, ErrTemplateTagNotFound):
		clientMsg := fmt.Sprintf("template %s has no ready build", label)
		if tag != nil {
			clientMsg = fmt.Sprintf("template %s with tag '%s' not found", label, *tag)
		}

		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: clientMsg,
			Err:       err,
		}
	case errors.Is(err, ErrTemplateNotFound):
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: fmt.Sprintf("template %s not found", label),
			Err:       err,
		}
	case errors.Is(err, ErrAccessDenied):
		return &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: fmt.Sprintf("you don't have access to template %s", label),
			Err:       err,
		}
	case errors.Is(err, ErrClusterMismatch):
		return &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: fmt.Sprintf("template %s is not available in the requested cluster", label),
			Err:       err,
		}
	default:
		return &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: fmt.Sprintf("error resolving template %s", label),
			Err:       err,
		}
	}
}

func formatTemplateRef(identifier, templateID string) string {
	if templateID == "" || templateID == identifier {
		return "'" + identifier + "'"
	}

	return fmt.Sprintf("'%s' (%s)", identifier, templateID)
}

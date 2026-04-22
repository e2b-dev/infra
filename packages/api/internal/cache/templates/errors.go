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

type errorOpts struct {
	tag        *string
	templateID string
}

type ErrorOption func(*errorOpts)

func WithTag(tag *string) ErrorOption {
	return func(o *errorOpts) { o.tag = tag }
}

func WithTemplateID(templateID string) ErrorOption {
	return func(o *errorOpts) { o.templateID = templateID }
}

func ErrorToAPIError(err error, identifier string, opts ...ErrorOption) *api.APIError {
	var o errorOpts
	for _, opt := range opts {
		opt(&o)
	}
	tag := o.tag
	label := FormatTemplateRef(identifier, o.templateID)

	switch {
	case errors.Is(err, ErrTemplateTagNotFound):
		var msg string
		if tag != nil {
			msg = fmt.Sprintf("template %s with tag '%s' not found", label, *tag)
		} else {
			msg = fmt.Sprintf("template %s has no ready build", label)
		}
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: msg,
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

func FormatTemplateRef(identifier, templateID string) string {
	if templateID == "" || templateID == identifier {
		return "'" + identifier + "'"
	}
	return fmt.Sprintf("'%s' (%s)", identifier, templateID)
}

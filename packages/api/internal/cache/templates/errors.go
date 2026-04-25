package templatecache

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

var (
	ErrTemplateNotFound = errors.New("template not found")
	ErrAccessDenied     = errors.New("access denied")
	ErrClusterMismatch  = errors.New("template not available in cluster")
)

type TemplateRef struct {
	Subject    string
	Identifier string
	TemplateID string
	Tag        *string
	Visible    bool
}

func ErrorToAPIError(err error, fallbackIdentifier string) *api.APIError {
	return ToAPIError(err, "template", fallbackIdentifier)
}

func ToAPIError(err error, subject, identifier string) *api.APIError {
	switch {
	case errors.Is(err, ErrTemplateNotFound):
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: fmt.Sprintf("%s not found", formatTemplateRef(subject, identifier, "")),
			Err:       err,
		}
	case errors.Is(err, ErrAccessDenied):
		return &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: fmt.Sprintf("you don't have access to %s", formatTemplateRef(subject, identifier, "")),
			Err:       err,
		}
	case errors.Is(err, ErrClusterMismatch):
		return &api.APIError{
			Code:      http.StatusBadRequest,
			ClientMsg: fmt.Sprintf("%s is not available in the requested cluster", formatTemplateRef(subject, identifier, "")),
			Err:       err,
		}
	}

	return &api.APIError{
		Code:      http.StatusInternalServerError,
		ClientMsg: fmt.Sprintf("error resolving %s", formatTemplateRef(subject, identifier, "")),
		Err:       err,
	}
}

func (r TemplateRef) APIError(err error) *api.APIError {
	if !r.Visible && (errors.Is(err, ErrAccessDenied) || errors.Is(err, ErrTemplateNotFound)) {
		return &api.APIError{
			Code:      http.StatusNotFound,
			ClientMsg: fmt.Sprintf("%s not found", formatTemplateRef(r.Subject, r.Identifier, "")),
			Err:       err,
		}
	}

	if !errors.Is(err, ErrTemplateNotFound) {
		return ToAPIError(err, r.Subject, r.Identifier)
	}

	label := formatTemplateRef(r.Subject, r.Identifier, r.TemplateID)
	clientMsg := fmt.Sprintf("%s has no ready build", label)
	if r.Tag != nil {
		clientMsg = fmt.Sprintf("%s with tag '%s' not found", label, *r.Tag)
	}

	return &api.APIError{
		Code:      http.StatusNotFound,
		ClientMsg: clientMsg,
		Err:       err,
	}
}

func formatTemplateRef(subject, identifier, templateID string) string {
	if subject == "" {
		subject = "template"
	}

	if templateID == "" || templateID == identifier {
		return fmt.Sprintf("%s '%s'", subject, identifier)
	}

	return fmt.Sprintf("%s '%s' (%s)", subject, identifier, templateID)
}

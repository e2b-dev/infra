package handlers

import (
	"errors"
	"net/http"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

// intentErrorToAPIError converts a blocked/forbidden error returned by
// auth.AuthorizeTeamCtx into the *api.APIError shape handlers use.
// Both states surface as 403; the message preserves any BlockedReason.
func intentErrorToAPIError(err error) *api.APIError {
	if err == nil {
		return nil
	}

	var blocked *auth.TeamBlockedError
	if errors.As(err, &blocked) {
		return &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: blocked.Error(),
			Err:       err,
		}
	}

	var forbidden *auth.TeamForbiddenError
	if errors.As(err, &forbidden) {
		return &api.APIError{
			Code:      http.StatusForbidden,
			ClientMsg: forbidden.Error(),
			Err:       err,
		}
	}

	return &api.APIError{
		Code:      http.StatusForbidden,
		ClientMsg: "Access denied",
		Err:       err,
	}
}

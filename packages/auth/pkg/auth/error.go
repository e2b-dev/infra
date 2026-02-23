package auth

import "github.com/e2b-dev/infra/packages/shared/pkg/apierrors"

// APIError is re-exported from apierrors so that auth internals can use it unqualified.
type APIError = apierrors.APIError

// TeamForbiddenError is returned when a team's access is forbidden (e.g. banned).
type TeamForbiddenError struct {
	Message string
}

func (e *TeamForbiddenError) Error() string {
	return e.Message
}

// TeamBlockedError is returned when a team is blocked.
type TeamBlockedError struct {
	Message string
}

func (e *TeamBlockedError) Error() string {
	return e.Message
}

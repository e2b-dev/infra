package auth

import (
	internalauthteam "github.com/e2b-dev/infra/packages/auth/internal/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
)

// APIError is re-exported from apierrors so that auth internals can use it unqualified.
type APIError = apierrors.APIError

// TeamForbiddenError is returned when a team's access is forbidden (e.g. banned).
type TeamForbiddenError = internalauthteam.ForbiddenError

// TeamBlockedError is returned when a team is blocked.
type TeamBlockedError = internalauthteam.BlockedError

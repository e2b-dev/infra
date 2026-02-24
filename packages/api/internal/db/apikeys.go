package db

import (
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

type TeamForbiddenError = sharedauth.TeamForbiddenError

type TeamBlockedError = sharedauth.TeamBlockedError

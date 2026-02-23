package auth

import sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"

const (
	TeamContextKey   = sharedauth.TeamContextKey
	UserIDContextKey = sharedauth.UserIDContextKey
)

var ErrNoAuthHeader = sharedauth.ErrNoAuthHeader

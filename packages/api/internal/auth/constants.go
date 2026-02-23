package auth

import sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"

const (
	TeamContextKey   = "team"
	UserIDContextKey = "user_id"
)

var ErrNoAuthHeader = sharedauth.ErrNoAuthHeader

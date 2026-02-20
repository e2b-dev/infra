package auth

import sharedauth "github.com/e2b-dev/infra/packages/shared/pkg/auth"

const (
	TeamContextKey   = "team"
	UserIDContextKey = "user_id"
)

var ErrNoAuthHeader = sharedauth.ErrNoAuthHeader

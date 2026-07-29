// Package management holds the state changes the control-plane management
// interface applies. They live outside the handlers so they are reachable
// without gin: what these operations get wrong is never the HTTP.
//
// Each one owns the cache evictions its own write invalidates, and reports
// failures as sentinel errors rather than database ones, so the routes above
// map outcomes to status codes without knowing what backs them.
package management

import (
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
)

type Service struct {
	db    *authdb.Client
	cache sharedauth.Service
}

func NewService(db *authdb.Client, cache sharedauth.Service) *Service {
	return &Service{db: db, cache: cache}
}

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
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
)

// Two clients, because the tables these operations write are reached through
// two pools: membership and its projection through the auth one, limits and
// theirs through the main one, which is where project_limits and the
// team_limits view that reads it live. No transaction spans both -- the two
// connection strings are configured separately and need not name one database.
type Service struct {
	db       *authdb.Client
	limitsDB *sqlcdb.Client
	cache    sharedauth.Service
}

func NewService(db *authdb.Client, limitsDB *sqlcdb.Client, cache sharedauth.Service) *Service {
	return &Service{db: db, limitsDB: limitsDB, cache: cache}
}

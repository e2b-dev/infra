package auth

import (
	"github.com/google/uuid"

	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
)

type SupabaseInfo struct {
	TeamInfo authcache.AuthTeamInfo
	UserID   uuid.UUID
}

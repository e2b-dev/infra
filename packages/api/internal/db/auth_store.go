package db

import (
	"context"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
)

// AuthStoreImpl implements sharedauth.AuthStore[*types.Team] using the auth DB client.
type AuthStoreImpl struct {
	authDB *authdb.Client
}

var _ sharedauth.AuthStore[*types.Team] = (*AuthStoreImpl)(nil)

// NewAuthStore creates a new AuthStoreImpl.
func NewAuthStore(authDB *authdb.Client) *AuthStoreImpl {
	return &AuthStoreImpl{authDB: authDB}
}

func (s *AuthStoreImpl) GetTeamByHashedAPIKey(ctx context.Context, hashedKey string) (*types.Team, error) {
	return GetTeamAuth(ctx, s.authDB, hashedKey)
}

func (s *AuthStoreImpl) GetTeamByIDAndUserID(ctx context.Context, userID uuid.UUID, teamID string) (*types.Team, error) {
	return GetTeamByIDAndUserIDAuth(ctx, s.authDB, teamID, userID)
}

func (s *AuthStoreImpl) GetUserIDByHashedAccessToken(ctx context.Context, hashedToken string) (uuid.UUID, error) {
	return s.authDB.Read.GetUserIDFromAccessToken(ctx, hashedToken)
}

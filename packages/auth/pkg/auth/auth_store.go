package auth

import (
	"context"

	"github.com/google/uuid"

	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/auth/pkg/auth")

type AuthStoreImpl struct {
	authDB *authdb.Client
}

var _ AuthStore[*types.Team] = (*AuthStoreImpl)(nil)

func NewAuthStore(authDB *authdb.Client) *AuthStoreImpl {
	return &AuthStoreImpl{authDB: authDB}
}

func (s *AuthStoreImpl) GetTeamByHashedAPIKey(ctx context.Context, hashedKey string) (*types.Team, error) {
		ctx, span := tracer.Start(ctx, "get team auth")
		defer span.End()

		result, err := db.Read.GetTeamWithTierByAPIKey(ctx, apiKey)
		if err != nil {
			errMsg := fmt.Errorf("failed to get team from API key: %w", err)

			return nil, errMsg
		}

		err = validateTeamUsage(result.Team)
		if err != nil {
			return nil, err
		}

		go func() {
			// Run the update in a separate context to avoid an extra latency
			ctx := context.WithoutCancel(ctx)
			updateErr := db.Write.UpdateLastTimeUsed(ctx, apiKey)
			if updateErr != nil {
				logger.L().Error(ctx, "failed to update last time used", zap.Error(updateErr))
			}
		}()

		team := types.NewTeam(&result.Team, &result.TeamLimit)

		return team, nil
}

func (s *AuthStoreImpl) GetTeamByIDAndUserID(ctx context.Context, userID uuid.UUID, teamID string) (*types.Team, error) {
	return GetTeamByIDAndUserIDAuth(ctx, s.authDB, teamID, userID)
}

func (s *AuthStoreImpl) GetUserIDByHashedAccessToken(ctx context.Context, hashedToken string) (uuid.UUID, error) {
	return s.authDB.Read.GetUserIDFromAccessToken(ctx, hashedToken)
}

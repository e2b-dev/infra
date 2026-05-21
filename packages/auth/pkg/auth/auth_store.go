package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/auth/pkg/auth")

type authStoreImpl struct {
	authDB *authdb.Client
}

var _ authStore = (*authStoreImpl)(nil)

func newAuthStore(authDB *authdb.Client) *authStoreImpl {
	return &authStoreImpl{authDB: authDB}
}

func (s *authStoreImpl) GetTeamByHashedAPIKey(ctx context.Context, hashedKey string) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team auth")
	defer span.End()

	result, err := s.authDB.Read.GetTeamWithTierByAPIKey(ctx, hashedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get team from API key: %w", err)
	}

	if err := CheckTeamBanned(result.Team); err != nil {
		return nil, err
	}

	go func() {
		// Run the update in a separate context to avoid an extra latency
		ctx := context.WithoutCancel(ctx)
		updateErr := s.authDB.Write.UpdateLastTimeUsed(ctx, hashedKey)
		if updateErr != nil {
			logger.L().Error(ctx, "failed to update last time used", zap.Error(updateErr))
		}
	}()

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

func (s *authStoreImpl) GetTeamByID(ctx context.Context, teamID uuid.UUID) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team by id auth")
	defer span.End()

	result, err := s.authDB.Read.GetTeamWithTierByTeamID(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to get team from team ID: %w", err)
	}

	if err := CheckTeamBanned(result.Team); err != nil {
		return nil, err
	}

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

func (s *authStoreImpl) GetTeamByIDAndUserID(ctx context.Context, userID uuid.UUID, teamID string) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team by id and user id auth")
	defer span.End()

	teamIDParsed, err := uuid.Parse(teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse team ID: %w", err)
	}

	result, err := s.authDB.Read.GetTeamWithTierByTeamAndUser(ctx, authqueries.GetTeamWithTierByTeamAndUserParams{
		ID:     teamIDParsed,
		UserID: userID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get team from teamID and userID key: %w", err)
	}

	if err := CheckTeamBanned(result.Team); err != nil {
		return nil, err
	}

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

func (s *authStoreImpl) GetUserIDByHashedAccessToken(ctx context.Context, hashedToken string) (uuid.UUID, error) {
	return s.authDB.Read.GetUserIDFromAccessToken(ctx, hashedToken)
}

func (s *authStoreImpl) GetTeamAPIKeyHashes(ctx context.Context, teamID uuid.UUID) ([]string, error) {
	return s.authDB.Read.GetTeamAPIKeyHashes(ctx, teamID)
}

package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/db/pkg/auth"
)

func GetTeamByID(ctx context.Context, db *authdb.Client, teamID uuid.UUID) (*types.Team, error) {
	result, err := db.Read.GetTeamWithTierByTeamID(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to get team by ID: %w", err)
	}

	return types.NewTeam(&result.Team, &result.TeamLimit), nil
}

func GetTeamsByUser(ctx context.Context, db *authdb.Client, userID uuid.UUID) ([]*types.TeamWithDefault, error) {
	teams, err := db.Read.GetTeamsWithUsersTeamsWithTier(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("error when getting default team: %w", err)
	}

	teamsWithLimits := make([]*types.TeamWithDefault, 0, len(teams))
	for _, team := range teams {
		teamsWithLimits = append(teamsWithLimits, &types.TeamWithDefault{
			Team:      types.NewTeam(&team.Team, &team.TeamLimit),
			IsDefault: team.UsersTeam.IsDefault,
		})
	}

	return teamsWithLimits, nil
}

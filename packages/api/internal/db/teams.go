package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	sqlcdb "github.com/e2b-dev/infra/packages/db/pkg/client"
)

func GetTeamsByUser(ctx context.Context, db *sqlcdb.Client, userID uuid.UUID) ([]*types.TeamWithDefault, error) {
	teams, err := db.GetTeamsWithUsersTeamsWithTier(ctx, userID)
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

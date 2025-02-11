package db

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/usersteams"
	"github.com/google/uuid"
)

func (db *DB) GetTeamByIDAndUserIDAuth(ctx context.Context, teamID string, userID uuid.UUID) (*models.Team, *models.Tier, error) {
	teamIDParsed, err := uuid.Parse(teamID)
	if err != nil {
		errMsg := fmt.Errorf("failed to parse team ID: %w", err)

		return nil, nil, errMsg
	}

	result, err := db.
		Client.
		Team.
		Query().
		WithUsersTeams().
		Where(
			team.ID(teamIDParsed),
			team.HasUsersTeamsWith(
				usersteams.UserID(userID),
			),
		).
		WithTeamTier().
		Only(ctx)
	if err != nil {
		errMsg := fmt.Errorf("failed to get team from teamID and userID key: %w", err)

		return nil, nil, errMsg
	}

	err = validateTeamUsage(result)
	if err != nil {
		return nil, nil, err
	}

	return result, result.Edges.TeamTier, nil
}

package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/user"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/usersteams"
)

func (db *DB) GetTeams(ctx context.Context, userID uuid.UUID) ([]*models.Team, error) {
	t, err := db.
		Client.
		Team.
		Query().
		Select(team.FieldID).
		Where(team.HasUsersWith(user.ID(userID))).
		WithTeamTier().
		WithUsersTeams(func(query *models.UsersTeamsQuery) {
			query.Where(usersteams.UserID(userID))
		}).
		All(ctx)

	if err != nil {
		errMsg := fmt.Errorf("failed to get default team from user: %w", err)

		return nil, errMsg
	}

	return t, nil
}

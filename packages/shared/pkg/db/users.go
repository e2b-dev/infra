package db

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/team"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/user"

	"github.com/google/uuid"
)

func (db *DB) GetDefaultTeamFromUserID(ctx context.Context, userID uuid.UUID) (t *models.Team, err error) {
	t, err = db.
		Client.
		Team.
		Query().
		Select(team.FieldID).
		Where(team.IsDefaultEQ(true), team.HasUsersWith(user.ID(userID))).
		Only(ctx)
	if err != nil {
		errMsg := fmt.Errorf("failed to get default team from user: %w", err)

		return nil, errMsg
	}

	return t, nil
}

func (db *DB) GetDefaultTeamAndTierFromUserID(ctx context.Context, userID uuid.UUID) (*models.Team, error) {
	t, err := db.
		Client.
		Team.
		Query().
		Select(team.FieldID).
		Where(team.IsDefaultEQ(true), team.HasUsersWith(user.ID(userID))).
		WithTeamTier().
		Only(ctx)

	if err != nil {
		errMsg := fmt.Errorf("failed to get default team from user: %w", err)

		return nil, errMsg
	}

	return t, nil
}

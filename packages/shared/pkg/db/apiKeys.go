package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/accesstoken"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/teamapikey"
)

func (db *DB) GetUserID(ctx context.Context, token string) (*uuid.UUID, error) {
	result, err := db.
		Client.
		AccessToken.
		Query().
		Where(accesstoken.AccessTokenHash(hashedToken)).
		Only(ctx)
	if err != nil {
		errMsg := fmt.Errorf("failed to get user from access token: %w", err)

		return nil, errMsg
	}

	return &result.UserID, nil
}

func (db *DB) GetTeamAPIKeys(ctx context.Context, teamID uuid.UUID) ([]*models.TeamAPIKey, error) {
	result, err := db.
		Client.
		TeamAPIKey.
		Query().
		Where(teamapikey.TeamID(teamID)).
		All(ctx)
	if err != nil {
		errMsg := fmt.Errorf("failed to get team API keys: %w", err)

		return nil, errMsg
	}

	return result, nil
}

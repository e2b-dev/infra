package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/accesstoken"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/teamapikey"
)

func (db *DB) GetTeamAuth(ctx context.Context, apiKey string) (*models.Team, *models.Tier, error) {
	result, err := db.
		Client.
		TeamAPIKey.
		Query().
		WithTeam().
		Where(teamapikey.ID(apiKey)).
		QueryTeam().
		WithTeamTier().
		Only(ctx)

	if err != nil {
		errMsg := fmt.Errorf("failed to get team from API key: %w", err)

		return nil, nil, errMsg
	}
	//
	if result.IsBanned {
		errMsg := fmt.Errorf("team is banned")

		return nil, nil, errMsg
	}
	//
	if result.IsBlocked {
		if result.BlockedReason == nil {
			errMsg := fmt.Errorf("team was blocked")

			return nil, nil, errMsg
		}

		errMsg := fmt.Errorf("team was blocked - %s", *result.BlockedReason)

		return nil, nil, errMsg
	}
	//
	return result, result.Edges.TeamTier, nil
}

func (db *DB) GetUserID(ctx context.Context, token string) (*uuid.UUID, error) {
	result, err := db.
		Client.
		AccessToken.
		Query().
		Where(accesstoken.ID(token)).
		Only(ctx)

	if err != nil {
		errMsg := fmt.Errorf("failed to get user from access token: %w", err)

		return nil, errMsg
	}

	return &result.UserID, nil
}

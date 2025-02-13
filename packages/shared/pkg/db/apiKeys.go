package db

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/accesstoken"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/teamapikey"
	"github.com/google/uuid"
)

type TeamUsageError struct {
	message string
}

func (e *TeamUsageError) Error() string {
	return e.message
}

func validateTeamUsage(team *models.Team) error {
	if team.IsBanned {
		return &TeamUsageError{message: "team is banned"}
	}

	if team.IsBlocked {
		if team.BlockedReason == nil {
			return &TeamUsageError{message: "team was blocked"}
		}

		return &TeamUsageError{message: fmt.Sprintf("team was blocked, reason: %s", *team.BlockedReason)}
	}

	return nil
}

func (db *DB) GetTeamAuth(ctx context.Context, apiKey string) (*models.Team, *models.Tier, error) {
	result, err := db.
		Client.
		TeamAPIKey.
		Query().
		WithTeam().
		Where(teamapikey.APIKey(apiKey)).
		QueryTeam().
		WithTeamTier().
		Only(ctx)

	if err != nil {
		errMsg := fmt.Errorf("failed to get team from API key: %w", err)

		return nil, nil, errMsg
	}

	err = validateTeamUsage(result)
	if err != nil {
		return nil, nil, err
	}

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

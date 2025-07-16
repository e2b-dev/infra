package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
)

func GetTeamByIDAndUserIDAuth(ctx context.Context, db *sqlcdb.Client, teamID string, userID uuid.UUID) (*queries.Team, *queries.Tier, error) {
	teamIDParsed, err := uuid.Parse(teamID)
	if err != nil {
		errMsg := fmt.Errorf("failed to parse team ID: %w", err)

		return nil, nil, errMsg
	}

	result, err := db.GetTeamWithTierByTeamAndUser(ctx, queries.GetTeamWithTierByTeamAndUserParams{
		ID:     teamIDParsed,
		UserID: userID,
	})
	if err != nil {
		errMsg := fmt.Errorf("failed to get team from teamID and userID key: %w", err)

		return nil, nil, errMsg
	}

	err = validateTeamUsage(result.Team)
	if err != nil {
		return nil, nil, err
	}

	return &result.Team, &result.Tier, nil
}

package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
)

func GetTeamByIDAndUserIDAuth(ctx context.Context, db *authdb.Client, teamID string, userID uuid.UUID) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team by id and user id auth")
	defer span.End()

	teamIDParsed, err := uuid.Parse(teamID)
	if err != nil {
		errMsg := fmt.Errorf("failed to parse team ID: %w", err)

		return nil, errMsg
	}

	result, err := db.Read.GetTeamWithTierByTeamAndUser(ctx, authqueries.GetTeamWithTierByTeamAndUserParams{
		ID:     teamIDParsed,
		UserID: userID,
	})
	if err != nil {
		errMsg := fmt.Errorf("failed to get team from teamID and userID key: %w", err)

		return nil, errMsg
	}

	err = validateTeamUsage(result.Team)
	if err != nil {
		return nil, err
	}

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

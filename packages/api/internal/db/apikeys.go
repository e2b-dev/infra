package db

import (
	"context"
	"fmt"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
)

type TeamForbiddenError struct {
	message string
}

func (e *TeamForbiddenError) Error() string {
	return e.message
}

type TeamBlockedError struct {
	message string
}

func (e *TeamBlockedError) Error() string {
	return e.message
}

func validateTeamUsage(team queries.Team) error {
	if team.IsBanned {
		return &TeamForbiddenError{message: "team is banned"}
	}

	if team.IsBlocked {
		return &TeamBlockedError{message: "team is blocked"}
	}

	return nil
}

func GetTeamAuth(ctx context.Context, db *sqlcdb.Client, apiKey string) (*queries.Team, *queries.Tier, error) {
	result, err := db.GetTeamWithTierByAPIKey(ctx, apiKey)
	if err != nil {
		errMsg := fmt.Errorf("failed to get team from API key: %w", err)

		return nil, nil, errMsg
	}

	err = validateTeamUsage(result.Team)
	if err != nil {
		return nil, nil, err
	}

	return &result.Team, &result.Tier, nil
}

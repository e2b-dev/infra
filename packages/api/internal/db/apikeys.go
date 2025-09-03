package db

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

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

	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()

		// Log the usage of the API key without blocking the main flow
		err := db.UpdateTeamApiKeyLastUsed(ctx, apiKey)
		if err != nil {
			zap.L().Error("failed to update team api key last used", zap.Error(err))
		}
	}()

	err = validateTeamUsage(result.Team)
	if err != nil {
		return nil, nil, err
	}

	return &result.Team, &result.Tier, nil
}

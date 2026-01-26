package db

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/db")

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

func validateTeamUsage(team authqueries.Team) error {
	if team.IsBanned {
		return &TeamForbiddenError{message: "team is banned"}
	}

	if team.IsBlocked {
		return &TeamBlockedError{message: "team is blocked"}
	}

	return nil
}

func GetTeamAuth(ctx context.Context, db *authdb.Client, apiKey string) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team auth")
	defer span.End()

	result, err := db.Read.GetTeamWithTierByAPIKey(ctx, apiKey)
	if err != nil {
		errMsg := fmt.Errorf("failed to get team from API key: %w", err)

		return nil, errMsg
	}

	err = validateTeamUsage(result.Team)
	if err != nil {
		return nil, err
	}

	go func() {
		// Run the update in a separate context to avoid an extra latency
		ctx := context.WithoutCancel(ctx)
		updateErr := db.Write.UpdateLastTimeUsed(ctx, apiKey)
		if updateErr != nil {
			logger.L().Error(ctx, "failed to update last time used", zap.Error(updateErr))
		}
	}()

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

package team

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func CreateAPIKey(ctx context.Context, db *db.DB, teamID uuid.UUID, userID uuid.UUID, name string) (*models.TeamAPIKey, error) {
	teamApiKey := auth.GenerateTeamAPIKey()
	apiKey, err := db.Client.TeamAPIKey.
		Create().
		SetTeamID(teamID).
		SetCreatedBy(userID).
		SetLastUsed(time.Now()).
		SetUpdatedAt(time.Now()).
		SetAPIKey(teamApiKey).
		SetAPIKeyMask(auth.MaskAPIKey(teamApiKey)).
		SetAPIKeyHash(auth.HashAPIKey(teamApiKey)).
		SetCreatedAt(time.Now()).
		SetName(name).
		Save(ctx)
	if err != nil {
		errMsg := fmt.Errorf("error when creating API key: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
	}

	return apiKey, nil
}

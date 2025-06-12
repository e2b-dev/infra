package team

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func CreateAPIKey(ctx context.Context, db *db.DB, teamID uuid.UUID, userID uuid.UUID, name string) (*models.TeamAPIKey, error) {
	teamApiKey, err := keys.GenerateKey(keys.ApiKeyPrefix)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when generating team API key", err)

		return nil, fmt.Errorf("error when generating team API key: %w", err)
	}

	apiKey, err := db.Client.TeamAPIKey.
		Create().
		SetTeamID(teamID).
		SetCreatedBy(userID).
		SetLastUsed(time.Now()).
		SetUpdatedAt(time.Now()).
		SetAPIKey(teamApiKey.PrefixedRawValue).
		SetAPIKeyHash(teamApiKey.HashedValue).
		SetAPIKeyPrefix(teamApiKey.Masked.Prefix).
		SetAPIKeyLength(teamApiKey.Masked.ValueLength).
		SetAPIKeyMaskPrefix(teamApiKey.Masked.MaskedValuePrefix).
		SetAPIKeyMaskSuffix(teamApiKey.Masked.MaskedValueSuffix).
		SetName(name).
		Save(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when creating API key", err)

		return nil, fmt.Errorf("error when creating API key: %w", err)
	}

	return apiKey, nil
}

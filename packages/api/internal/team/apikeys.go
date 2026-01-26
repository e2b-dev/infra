package team

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type CreateAPIKeyResponse struct {
	*authqueries.TeamApiKey

	RawAPIKey string
}

func CreateAPIKey(ctx context.Context, authDB *authdb.Client, teamID uuid.UUID, userID uuid.UUID, name string) (CreateAPIKeyResponse, error) {
	teamApiKey, err := keys.GenerateKey(keys.ApiKeyPrefix)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when generating team API key", err)

		return CreateAPIKeyResponse{}, fmt.Errorf("error when generating team API key: %w", err)
	}

	apiKey, err := authDB.Write.CreateTeamAPIKey(ctx, authqueries.CreateTeamAPIKeyParams{
		TeamID:           teamID,
		CreatedBy:        &userID,
		ApiKeyHash:       teamApiKey.HashedValue,
		ApiKeyPrefix:     teamApiKey.Masked.Prefix,
		ApiKeyLength:     int32(teamApiKey.Masked.ValueLength),
		ApiKeyMaskPrefix: teamApiKey.Masked.MaskedValuePrefix,
		ApiKeyMaskSuffix: teamApiKey.Masked.MaskedValueSuffix,
		Name:             name,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when creating API key", err)

		return CreateAPIKeyResponse{}, fmt.Errorf("error when creating API key: %w", err)
	}

	return CreateAPIKeyResponse{
		TeamApiKey: &apiKey,
		RawAPIKey:  teamApiKey.PrefixedRawValue,
	}, nil
}

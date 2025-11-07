package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

var (
	tokenID        = uuid.MustParse("3d98c426-d348-446b-bdf6-5be3ca4123e2")
	userTokenValue = "89215020937a4c989cde33d7bc647715"
	teamTokenValue = "53ae1fed82754c17ad8077fbc8bcdd90"
	userID         = uuid.MustParse("fb69f46f-eb51-4a87-a14e-306f7a3fd89c")
)

func main() {
	ctx := context.Background()

	if err := run(ctx); err != nil {
		panic(err)
	}
}

func run(ctx context.Context) error {
	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")

	if connectionString == "" {
		if err := os.Setenv(
			"POSTGRES_CONNECTION_STRING",
			"postgresql://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
		); err != nil {
			return fmt.Errorf("failed to set environment variable: %w", err)
		}
	}

	sqlcDB, err := client.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer sqlcDB.Close()

	// create user
	if err := upsertUser(ctx, sqlcDB); err != nil {
		return fmt.Errorf("failed to upsert user: %w", err)
	}

	// create team
	teamID, err := upsertTeam(ctx, sqlcDB)
	if err != nil {
		return fmt.Errorf("failed to upsert team: %w", err)
	}

	if err = ensureUserIsOnTeam(ctx, sqlcDB, teamID); err != nil {
		return fmt.Errorf("failed to ensure user is on team: %w", err)
	}

	// create user token
	if err = upsertUserToken(ctx, sqlcDB, keys.AccessTokenPrefix, userTokenValue); err != nil {
		return fmt.Errorf("failed to upsert token: %w", err)
	}

	// create team token
	if err = upsertTeamAPIKey(ctx, sqlcDB, teamID, keys.ApiKeyPrefix, teamTokenValue); err != nil {
		return fmt.Errorf("failed to upsert token: %w", err)
	}

	// create local cluster
	// if err = upsertLocalCluster(ctx, sqlcDB); err != nil {
	//	return fmt.Errorf("failed to upsert local cluster: %w", err)
	// }

	return nil
}

func upsertTeamAPIKey(ctx context.Context, sqlcDB *client.Client, teamID uuid.UUID, tokenPrefix, token string) error {
	tokenHash, tokenMask, err := createTokenHash(tokenPrefix, token)
	if err != nil {
		return fmt.Errorf("failed to create token hash: %w", err)
	}

	if _, err = sqlcDB.CreateTeamAPIKey(ctx, queries.CreateTeamAPIKeyParams{
		TeamID:           teamID,
		CreatedBy:        &userID,
		ApiKeyHash:       tokenHash,
		ApiKeyPrefix:     tokenMask.Prefix,
		ApiKeyLength:     int32(tokenMask.ValueLength),
		ApiKeyMaskPrefix: tokenMask.MaskedValuePrefix,
		ApiKeyMaskSuffix: tokenMask.MaskedValueSuffix,
		Name:             "local dev seed token",
	}); ignoreConstraints(err) != nil {
		return fmt.Errorf("failed to create team api key: %w", err)
	}

	return nil
}

func ensureUserIsOnTeam(ctx context.Context, sqlcDB *client.Client, teamID uuid.UUID) error {
	if err := sqlcDB.TestsRawSQL(ctx, `
INSERT INTO users_teams (user_id, team_id, is_default)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING
`, userID, teamID, true); ignoreConstraints(err) != nil {
		return fmt.Errorf("failed to add user to team: %w", err)
	}

	return nil
}

func upsertUserToken(ctx context.Context, sqlcDB *client.Client, tokenPrefix, token string) error {
	tokenHash, tokenMask, err := createTokenHash(tokenPrefix, token)
	if err != nil {
		return fmt.Errorf("failed to create token hash: %w", err)
	}

	if _, err = sqlcDB.CreateAccessToken(ctx, queries.CreateAccessTokenParams{
		ID:                    tokenID,
		UserID:                userID,
		AccessTokenHash:       tokenHash,
		AccessTokenPrefix:     tokenMask.Prefix,
		AccessTokenLength:     int32(tokenMask.ValueLength),
		AccessTokenMaskPrefix: tokenMask.MaskedValuePrefix,
		AccessTokenMaskSuffix: tokenMask.MaskedValueSuffix,
		Name:                  "local dev seed token",
	}); ignoreConstraints(err) != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}

	return nil
}

func ignoreConstraints(err error) error {
	// sqlc check
	var pgconnErr *pgconn.PgError
	if errors.As(err, &pgconnErr) {
		if pgconnErr.Code == "23505" {
			return nil
		}
	}

	return err
}

func upsertTeam(ctx context.Context, sqlcDB *client.Client) (uuid.UUID, error) {
	teamID := uuid.MustParse("0b8a3ded-4489-4722-afd1-1d82e64ec2d5")

	err := sqlcDB.TestsRawSQL(ctx, `
INSERT INTO teams (id, email, name, tier, is_blocked)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
	email = EXCLUDED.email,
	name = EXCLUDED.name,
	tier = EXCLUDED.tier
`, teamID, "team@e2b-dev.local", "local-dev team", "base_v1", false)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to upsert team: %w", err)
	}

	return teamID, nil
}

func upsertUser(ctx context.Context, sqlcDB *client.Client) error {
	err := sqlcDB.TestsRawSQL(ctx, `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET
	email = EXCLUDED.email
`, userID, "user@e2b-dev.local")
	if err != nil {
		return fmt.Errorf("failed to upsert user: %w", err)
	}

	return nil
}

func createTokenHash(prefix, accessToken string) (string, keys.MaskedIdentifier, error) {
	hasher := keys.NewSHA256Hashing()
	tokenWithoutPrefix := strings.TrimPrefix(accessToken, prefix)
	accessTokenBytes, err := hex.DecodeString(tokenWithoutPrefix)
	if err != nil {
		return "", keys.MaskedIdentifier{}, fmt.Errorf("failed to hex decode string")
	}
	accessTokenHash := hasher.Hash(accessTokenBytes)
	accessTokenMask, err := keys.MaskKey(prefix, tokenWithoutPrefix)
	if err != nil {
		return "", keys.MaskedIdentifier{}, fmt.Errorf("failed to mask key")
	}

	return accessTokenHash, accessTokenMask, nil
}

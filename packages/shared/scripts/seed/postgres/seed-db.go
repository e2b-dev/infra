package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func main() {
	ctx := context.Background()
	hasher := keys.NewSHA256Hashing()

	sqlcDB, err := sqlcdb.NewClient(ctx)
	if err != nil {
		panic(err)
	}
	defer sqlcDB.Close()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Error getting home directory:", err)

		return
	}

	configPath := filepath.Join(homeDir, ".e2b", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		panic(err)
	}

	config := map[string]any{}
	err = json.Unmarshal(data, &config)
	if err != nil {
		panic(err)
	}

	email := config["email"].(string)
	teamID := config["teamId"].(string)
	accessToken := config["accessToken"].(string)
	teamAPIKey := config["teamApiKey"].(string)
	teamUUID := uuid.MustParse(teamID)

	// Open .e2b/config.json
	// Delete existing user and recreate (simpler for seeding)
	err = sqlcDB.TestsRawSQL(ctx, `
DELETE FROM auth.users WHERE email = $1
`, email)
	if err != nil {
		panic(err)
	}

	// Create the user
	userID := uuid.New()
	err = sqlcDB.TestsRawSQL(ctx, `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, email)
	if err != nil {
		panic(err)
	}

	// Delete team
	err = sqlcDB.TestsRawSQL(ctx, `
DELETE FROM teams WHERE email = $1
`, email)
	if err != nil {
		panic(err)
	}

	// Create team
	err = sqlcDB.TestsRawSQL(ctx, `
INSERT INTO teams (id, email, name, tier, is_blocked)
VALUES ($1, $2, $3, $4, $5)
`, teamUUID, email, "E2B", "base_v1", false)
	if err != nil {
		panic(err)
	}

	// Create user team
	err = sqlcDB.TestsRawSQL(ctx, `
INSERT INTO users_teams (user_id, team_id, is_default)
VALUES ($1, $2, $3)
`, userID, teamUUID, true)
	if err != nil {
		panic(err)
	}

	// Create access token
	tokenWithoutPrefix := strings.TrimPrefix(accessToken, keys.AccessTokenPrefix)
	accessTokenBytes, err := hex.DecodeString(tokenWithoutPrefix)
	if err != nil {
		panic(err)
	}
	accessTokenHash := hasher.Hash(accessTokenBytes)
	accessTokenMask, err := keys.MaskKey(keys.AccessTokenPrefix, tokenWithoutPrefix)
	if err != nil {
		panic(err)
	}
	_, err = sqlcDB.CreateAccessToken(
		ctx, queries.CreateAccessTokenParams{
			ID:                    uuid.New(),
			UserID:                userID,
			AccessTokenHash:       accessTokenHash,
			AccessTokenPrefix:     accessTokenMask.Prefix,
			AccessTokenLength:     int32(accessTokenMask.ValueLength),
			AccessTokenMaskPrefix: accessTokenMask.MaskedValuePrefix,
			AccessTokenMaskSuffix: accessTokenMask.MaskedValueSuffix,
			Name:                  "Seed Access Token",
		})
	if err != nil {
		panic(err)
	}

	// Create team api key
	keyWithoutPrefix := strings.TrimPrefix(teamAPIKey, keys.ApiKeyPrefix)
	apiKeyBytes, err := hex.DecodeString(keyWithoutPrefix)
	if err != nil {
		panic(err)
	}
	apiKeyHash := hasher.Hash(apiKeyBytes)
	apiKeyMask, err := keys.MaskKey(keys.ApiKeyPrefix, keyWithoutPrefix)
	if err != nil {
		panic(err)
	}
	_, err = sqlcDB.CreateTeamAPIKey(ctx, queries.CreateTeamAPIKeyParams{
		TeamID:           teamUUID,
		CreatedBy:        &userID,
		ApiKeyHash:       apiKeyHash,
		ApiKeyPrefix:     apiKeyMask.Prefix,
		ApiKeyLength:     int32(apiKeyMask.ValueLength),
		ApiKeyMaskPrefix: apiKeyMask.MaskedValuePrefix,
		ApiKeyMaskSuffix: apiKeyMask.MaskedValueSuffix,
		Name:             "Seed API Key",
	})
	if err != nil {
		panic(err)
	}

	// Create init template
	err = sqlcDB.TestsRawSQL(ctx, `
INSERT INTO envs (id, team_id, public, build_count, spawn_count, updated_at)
VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP)
`, "rki5dems9wqfm4r03t7g", teamUUID, true, 0, 0)
	if err != nil {
		panic(err)
	}
	// Run from make file and build base env

	fmt.Printf("Database seeded.\n")
}

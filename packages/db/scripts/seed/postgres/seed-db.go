package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func main() {
	ctx := context.Background()
	hasher := keys.NewSHA256Hashing()

	// Prompt user for values
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\nPlease enter the following values:")
	fmt.Println()

	fmt.Printf("Email: ")
	email, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("Error reading input:", err)

		return
	}

	email = strings.TrimSpace(email)
	if email == "" {
		fmt.Println("Error: Email cannot be empty")

		return
	}

	teamUUID := uuid.New()

	accessToken, err := keys.GenerateKey(keys.AccessTokenPrefix)
	if err != nil {
		fmt.Println("Error generating access token:", err)

		return
	}

	teamAPIKey, err := keys.GenerateKey(keys.ApiKeyPrefix)
	if err != nil {
		fmt.Printf("Error: %v\n", err)

		return
	}

	fmt.Println()
	fmt.Println("Seeding database with:")
	fmt.Printf("  Email: %s\n", email)
	fmt.Printf("  Team ID: %s\n", teamUUID)
	fmt.Printf("  Access Token: %s\n", accessToken.PrefixedRawValue)
	fmt.Printf("  Team API Key: %s\n", teamAPIKey.PrefixedRawValue)
	fmt.Println()

	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	db, err := client.NewClient(ctx, connectionString)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	authDb, err := authdb.NewClient(ctx, connectionString, connectionString)
	if err != nil {
		panic(err)
	}
	defer authDb.Close()

	// Open .e2b/config.json
	// Delete existing user and recreate (simpler for seeding)
	err = authDb.TestsRawSQL(ctx, `
DELETE FROM auth.users WHERE email = $1
`, email)
	if err != nil {
		panic(err)
	}

	// Create the user
	userID := uuid.New()
	err = authDb.TestsRawSQL(ctx, `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, email)
	if err != nil {
		panic(err)
	}

	// Delete team
	err = authDb.TestsRawSQL(ctx, `
DELETE FROM teams WHERE email = $1
`, email)
	if err != nil {
		panic(err)
	}

	// Create team
	err = authDb.TestsRawSQL(ctx, `
INSERT INTO teams (id, email, name, tier, is_blocked, slug)
VALUES ($1, $2, $3, $4, $5, $6)
`, teamUUID, email, "E2B", "base_v1", false, "e2b")
	if err != nil {
		panic(err)
	}

	// Create user team
	err = authDb.TestsRawSQL(ctx, `
INSERT INTO users_teams (user_id, team_id, is_default)
VALUES ($1, $2, $3)
`, userID, teamUUID, true)
	if err != nil {
		panic(err)
	}

	// Create access token
	tokenWithoutPrefix := strings.TrimPrefix(accessToken.PrefixedRawValue, keys.AccessTokenPrefix)
	accessTokenBytes, err := hex.DecodeString(tokenWithoutPrefix)
	if err != nil {
		panic(err)
	}
	accessTokenHash := hasher.Hash(accessTokenBytes)
	accessTokenMask, err := keys.MaskKey(keys.AccessTokenPrefix, tokenWithoutPrefix)
	if err != nil {
		panic(err)
	}
	_, err = authDb.Write.CreateAccessToken(
		ctx, authqueries.CreateAccessTokenParams{
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
	keyWithoutPrefix := strings.TrimPrefix(teamAPIKey.PrefixedRawValue, keys.ApiKeyPrefix)
	apiKeyBytes, err := hex.DecodeString(keyWithoutPrefix)
	if err != nil {
		panic(err)
	}
	apiKeyHash := hasher.Hash(apiKeyBytes)
	apiKeyMask, err := keys.MaskKey(keys.ApiKeyPrefix, keyWithoutPrefix)
	if err != nil {
		panic(err)
	}
	_, err = authDb.Write.CreateTeamAPIKey(ctx, authqueries.CreateTeamAPIKeyParams{
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

	fmt.Printf("Database seeded.\n")
}

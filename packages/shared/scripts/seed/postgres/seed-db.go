package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

func promptWithDefault(reader *bufio.Reader, prompt, defaultValue string) string {
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultValue)
	} else {
		fmt.Printf("%s: ", prompt)
	}

	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println("Error reading input:", err)

		return defaultValue
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue
	}

	return input
}

func promptDefaultOrGenerate(reader *bufio.Reader, prompt, defaultValue string, generateDefault func() (string, error)) (string, error) {
	if defaultValue != "" {
		fmt.Printf("%s:\n", prompt)
		fmt.Printf("  [1] Use existing: %s\n", defaultValue)
		fmt.Printf("  [2] Generate new\n")
		fmt.Printf("Choice [1]: ")

		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("error reading input: %w", err)
		}

		input = strings.TrimSpace(input)
		if input == "" || input == "1" {
			return defaultValue, nil
		}

		if input == "2" {
			// Generate new
			return generateDefault()
		}

		return "", fmt.Errorf("invalid choice: %s", input)
	}

	// No default, generate new
	return generateDefault()
}

func main() {
	ctx := context.Background()
	hasher := keys.NewSHA256Hashing()

	// Try to read config file for defaults
	configDefaults := make(map[string]string)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Error getting home directory:", err)

		return
	}
	configPath := filepath.Join(homeDir, ".e2b", "config.json")
	data, err := os.ReadFile(configPath)
	if err == nil {
		config := map[string]any{}
		if err := json.Unmarshal(data, &config); err == nil {
			if email, ok := config["email"].(string); ok {
				configDefaults["email"] = email
			}
			if teamID, ok := config["teamId"].(string); ok {
				configDefaults["teamId"] = teamID
			}
			if accessToken, ok := config["accessToken"].(string); ok {
				configDefaults["accessToken"] = accessToken
			}
			if teamAPIKey, ok := config["teamApiKey"].(string); ok {
				configDefaults["teamApiKey"] = teamAPIKey
			}
			fmt.Println("Loaded defaults from ~/.e2b/config.json")
		}
	}

	// Prompt user for values
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\nPlease enter the following values (press Enter to use default):")
	fmt.Println()

	email := promptWithDefault(reader, "Email", configDefaults["email"])
	teamIDStr, err := promptDefaultOrGenerate(reader, "Team ID", configDefaults["teamId"], func() (string, error) {
		return uuid.New().String(), nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)

		return
	}

	accessToken, err := promptDefaultOrGenerate(reader, "Access Token", configDefaults["accessToken"], func() (string, error) {
		key, err := keys.GenerateKey(keys.AccessTokenPrefix)
		if err != nil {
			return "", err
		}

		return key.PrefixedRawValue, nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)

		return
	}

	teamAPIKey, err := promptDefaultOrGenerate(reader, "Team API Key", configDefaults["teamApiKey"], func() (string, error) {
		key, err := keys.GenerateKey(keys.ApiKeyPrefix)
		if err != nil {
			return "", err
		}

		return key.PrefixedRawValue, nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)

		return
	}

	// Validate and parse team UUID
	var teamUUID uuid.UUID
	teamUUID, err = uuid.Parse(teamIDStr)
	if err != nil {
		fmt.Printf("Error: Invalid Team ID UUID: %v\n", err)

		return
	}

	fmt.Println()
	fmt.Println("Seeding database with:")
	fmt.Printf("  Email: %s\n", email)
	fmt.Printf("  Team ID: %s\n", teamUUID)
	fmt.Printf("  Access Token: %s\n", accessToken)
	fmt.Printf("  Team API Key: %s\n", teamAPIKey)
	fmt.Println()

	db, err := client.NewClient(ctx)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Open .e2b/config.json
	// Delete existing user and recreate (simpler for seeding)
	err = db.TestsRawSQL(ctx, `
DELETE FROM auth.users WHERE email = $1
`, email)
	if err != nil {
		panic(err)
	}

	// Create the user
	userID := uuid.New()
	err = db.TestsRawSQL(ctx, `
INSERT INTO auth.users (id, email)
VALUES ($1, $2)
`, userID, email)
	if err != nil {
		panic(err)
	}

	// Delete team
	err = db.TestsRawSQL(ctx, `
DELETE FROM teams WHERE email = $1
`, email)
	if err != nil {
		panic(err)
	}

	// Create team
	err = db.TestsRawSQL(ctx, `
INSERT INTO teams (id, email, name, tier, is_blocked)
VALUES ($1, $2, $3, $4, $5)
`, teamUUID, email, "E2B", "base_v1", false)
	if err != nil {
		panic(err)
	}

	// Create user team
	err = db.TestsRawSQL(ctx, `
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
	_, err = db.CreateAccessToken(
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
	_, err = db.CreateTeamAPIKey(ctx, queries.CreateTeamAPIKeyParams{
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
	err = db.TestsRawSQL(ctx, `
INSERT INTO envs (id, team_id, public, build_count, spawn_count, updated_at)
VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP)
`, "rki5dems9wqfm4r03t7g", teamUUID, true, 0, 0)
	if err != nil {
		panic(err)
	}
	// Run from make file and build base env

	fmt.Printf("Database seeded.\n")
}

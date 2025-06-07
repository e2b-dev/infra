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

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/accesstoken"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/team"
)

func main() {
	ctx := context.Background()
	hasher := keys.NewSHA256Hashing()

	database, err := db.NewClient(1, 1)
	if err != nil {
		panic(err)
	}
	defer database.Close()

	count, err := database.Client.Team.Query().Count(ctx)
	if err != nil {
		panic(err)
	}

	if count > 1 {
		panic("Database contains some non-trivial data.")
	}

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

	config := map[string]interface{}{}
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
	user, err := database.Client.User.Create().SetEmail(email).SetID(uuid.New()).Save(ctx)
	if err != nil {
		panic(err)
	}

	// Delete team
	_, err = database.Client.Team.Delete().Where(team.Email(email)).Exec(ctx)
	if err != nil {
		panic(err)
	}

	// Remove old access token
	_, err = database.Client.AccessToken.Delete().Where(accesstoken.UserID(user.ID)).Exec(ctx)
	if err != nil {
		panic(err)
	}

	// Create team
	t, err := database.Client.Team.Create().SetEmail(email).SetName("E2B").SetID(teamUUID).SetTier("base_v1").Save(ctx)
	if err != nil {
		panic(err)
	}

	// Create user team
	_, err = database.Client.UsersTeams.Create().SetUserID(user.ID).SetTeamID(t.ID).SetIsDefault(true).Save(ctx)
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
	_, err = database.Client.AccessToken.Create().
		SetUser(user).
		SetAccessToken(accessToken).
		SetAccessTokenHash(accessTokenHash).
		SetAccessTokenMask(accessTokenMask).
		SetName("Seed Access Token").
		Save(ctx)
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
	_, err = database.Client.TeamAPIKey.Create().
		SetTeam(t).
		SetAPIKey(teamAPIKey).
		SetAPIKeyHash(apiKeyHash).
		SetAPIKeyMask(apiKeyMask).
		SetName("Seed API Key").
		Save(ctx)
	if err != nil {
		panic(err)
	}

	// Create template
	_, err = database.Client.Env.Create().SetTeam(t).SetID("rki5dems9wqfm4r03t7g").SetPublic(true).Save(ctx)
	if err != nil {
		panic(err)
	}
	// Run from make file and build base env

	fmt.Printf("Database seeded.\n")
}

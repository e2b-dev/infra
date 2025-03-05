package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/accesstoken"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/team"
)

func main() {
	ctx := context.Background()

	database, err := db.NewClient()
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
	_, err = database.Client.AccessToken.Create().SetUser(user).SetID(accessToken).Save(ctx)
	if err != nil {
		panic(err)
	}

	// Create team api key
	_, err = database.Client.TeamAPIKey.Create().SetTeam(t).SetAPIKey(teamAPIKey).Save(ctx)
	if err != nil {
		panic(err)
	}

	// Create template
	data, err = os.ReadFile("e2b.toml")
	_, err = database.Client.Env.Create().SetTeam(t).SetID("rki5dems9wqfm4r03t7g").SetPublic(true).Save(ctx)
	if err != nil {
		panic(err)
	}
	// Run from make file and build base env

	fmt.Printf("Database seeded.\n")
}

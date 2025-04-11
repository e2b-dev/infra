package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
)

type SeedData struct {
	APIKey  string
	EnvID   string
	BuildID uuid.UUID
	TeamID  uuid.UUID
	UserID  uuid.UUID
}

func main() {
	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	if connectionString == "" {
		log.Fatalf("POSTGRES_CONNECTION_STRING is not set")
	}

	database, err := db.NewClient()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	data := SeedData{
		APIKey:  os.Getenv("TESTS_E2B_API_KEY"),
		EnvID:   os.Getenv("TESTS_SANDBOX_TEMPLATE_ID"),
		BuildID: uuid.MustParse(os.Getenv("TESTS_SANDBOX_BUILD_ID")),
		TeamID:  uuid.MustParse(os.Getenv("TESTS_SANDBOX_TEAM_ID")),
		UserID:  uuid.MustParse(os.Getenv("TESTS_SANDBOX_USER_ID")),
	}

	err = seed(database, data)
	if err != nil {
		log.Fatalf("Failed to execute seed: %v", err)
	}

	fmt.Println("Seed completed successfully.")
}

func seed(db *db.DB, data SeedData) error {
	ctx := context.Background()

	// User
	_, err := db.Client.User.Create().
		SetID(data.UserID).
		SetEmail("user-test-integration@e2b.dev").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// Team
	t, err := db.Client.Team.Create().
		SetID(data.TeamID).
		SetEmail("test-integration@e2b.dev").
		SetName("E2B").
		SetTier("base_v1").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create team: %w", err)
	}

	// User-Team
	_, err = db.Client.UsersTeams.Create().
		SetUserID(data.UserID).
		SetTeamID(t.ID).
		SetIsDefault(true).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create user-team: %w", err)
	}

	// Team API Key
	hasher := keys.NewSHA256Hashing()
	keyWithoutPrefix := strings.TrimPrefix(data.APIKey, keys.ApiKeyPrefix)
	apiKeyBytes, err := hex.DecodeString(keyWithoutPrefix)
	if err != nil {
		return fmt.Errorf("failed to decode api key: %w", err)
	}
	hash := hasher.Hash(apiKeyBytes)
	mask, err := keys.MaskKey(keys.ApiKeyPrefix, keyWithoutPrefix)
	if err != nil {
		return fmt.Errorf("failed to mask api key: %w", err)
	}
	_, err = db.Client.TeamAPIKey.Create().
		SetTeam(t).
		SetAPIKey(data.APIKey).
		SetAPIKeyHash(hash).
		SetAPIKeyMask(mask).
		SetName("Integration Tests API Key").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create team api key: %w", err)
	}

	// Base image build
	_, err = db.Client.Env.Create().
		SetID(data.EnvID).
		SetTeamID(t.ID).
		SetPublic(false).
		SetBuildCount(1).
		SetSpawnCount(0).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create env: %w", err)
	}

	type buildData struct {
		id        uuid.UUID
		createdAt *time.Time
	}

	oldBuildTime := time.Now().Add(-time.Hour)
	builds := []buildData{
		{
			id:        data.BuildID,
			createdAt: nil,
		},
		// An older build, so we have multiple builds
		{
			id:        uuid.New(),
			createdAt: &oldBuildTime,
		},
	}

	for _, build := range builds {
		_, err = db.Client.EnvBuild.Create().
			SetID(build.id).
			SetEnvID(data.EnvID).
			SetDockerfile("FROM e2bdev/base:latest").
			SetStatus(envbuild.StatusUploaded).
			SetVcpu(2).
			SetRAMMB(512).
			SetFreeDiskSizeMB(512).
			SetTotalDiskSizeMB(1982).
			SetKernelVersion("vmlinux-6.1.102").
			SetFirecrackerVersion("v1.10.1_1fcdaec").
			SetEnvdVersion("0.2.0").
			SetNillableCreatedAt(build.createdAt).Save(ctx)
		if err != nil {
			return fmt.Errorf("failed to create env build: %w", err)
		}
	}

	_, err = db.Client.EnvAlias.Create().
		SetID("base").
		SetIsRenamable(true).
		SetEnvID(data.EnvID).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create env alias: %w", err)
	}

	return nil
}

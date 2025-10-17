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

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
)

type SeedData struct {
	AccessToken string
	APIKey      string
	EnvID       string
	BuildID     uuid.UUID
	TeamID      uuid.UUID
	UserID      uuid.UUID
}

func main() {
	ctx := context.Background()

	exitCode := run(ctx)
	if exitCode != 0 {
		log.Printf("Seed failed with exit code %d", exitCode)
		os.Exit(exitCode)
	}

	fmt.Println("Seed completed successfully.")
}

func run(ctx context.Context) int {
	connectionString := os.Getenv("POSTGRES_CONNECTION_STRING")
	if connectionString == "" {
		log.Printf("POSTGRES_CONNECTION_STRING is not set")
		return 1
	}

	dbPool, err := db.NewPool(ctx)
	if err != nil {
		log.Printf("Failed to connect to database: %v", err)
		return 1
	}

	dbConn := db.Open(dbPool)

	database := db.NewClient(dbConn)
	defer database.Close()

	sqlcDB := client.NewClient(dbPool)

	data := SeedData{
		AccessToken: os.Getenv("TESTS_E2B_ACCESS_TOKEN"),
		APIKey:      os.Getenv("TESTS_E2B_API_KEY"),
		EnvID:       os.Getenv("TESTS_SANDBOX_TEMPLATE_ID"),
		BuildID:     uuid.MustParse(os.Getenv("TESTS_SANDBOX_BUILD_ID")),
		TeamID:      uuid.MustParse(os.Getenv("TESTS_SANDBOX_TEAM_ID")),
		UserID:      uuid.MustParse(os.Getenv("TESTS_SANDBOX_USER_ID")),
	}

	err = seed(ctx, database, sqlcDB, data)
	if err != nil {
		log.Printf("Failed to execute seed: %v", err)
		return 1
	}

	fmt.Println("Seed completed successfully.")
	return 0
}

func seed(ctx context.Context, db *db.DB, sqlcDB *client.Client, data SeedData) error {
	hasher := keys.NewSHA256Hashing()

	// User
	user, err := db.Client.User.Create().
		SetID(data.UserID).
		SetEmail("user-test-integration@e2b.dev").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// Access token
	tokenWithoutPrefix := strings.TrimPrefix(data.AccessToken, keys.AccessTokenPrefix)
	accessTokenBytes, err := hex.DecodeString(tokenWithoutPrefix)
	if err != nil {
		return fmt.Errorf("failed to decode access token: %w", err)
	}

	accessTokenHash := hasher.Hash(accessTokenBytes)

	accessTokenMask, err := keys.MaskKey(keys.AccessTokenPrefix, tokenWithoutPrefix)
	if err != nil {
		return fmt.Errorf("failed to mask access token: %w", err)
	}

	err = db.Client.AccessToken.Create().
		SetUser(user).
		SetAccessTokenHash(accessTokenHash).
		SetAccessTokenPrefix(accessTokenMask.Prefix).
		SetAccessTokenLength(accessTokenMask.ValueLength).
		SetAccessTokenMaskPrefix(accessTokenMask.MaskedValuePrefix).
		SetAccessTokenMaskSuffix(accessTokenMask.MaskedValueSuffix).
		SetName("Integration Tests Access Token").
		Exec(ctx)
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
	keyWithoutPrefix := strings.TrimPrefix(data.APIKey, keys.ApiKeyPrefix)
	apiKeyBytes, err := hex.DecodeString(keyWithoutPrefix)
	if err != nil {
		return fmt.Errorf("failed to decode api key: %w", err)
	}
	apiKeyHash := hasher.Hash(apiKeyBytes)
	apiKeyMask, err := keys.MaskKey(keys.ApiKeyPrefix, keyWithoutPrefix)
	if err != nil {
		return fmt.Errorf("failed to mask api key: %w", err)
	}
	_, err = sqlcDB.CreateTeamAPIKey(ctx, queries.CreateTeamAPIKeyParams{
		TeamID:           t.ID,
		CreatedBy:        &data.UserID,
		ApiKeyHash:       apiKeyHash,
		ApiKeyPrefix:     apiKeyMask.Prefix,
		ApiKeyLength:     int32(apiKeyMask.ValueLength),
		ApiKeyMaskPrefix: apiKeyMask.MaskedValuePrefix,
		ApiKeyMaskSuffix: apiKeyMask.MaskedValueSuffix,
		Name:             "Integration Tests API Key",
	})
	if err != nil {
		return fmt.Errorf("failed to create team api key: %w", err)
	}

	// Base image build
	_, err = db.Client.Env.Create().
		SetID(data.EnvID).
		SetTeamID(t.ID).
		SetPublic(true).
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
			SetFirecrackerVersion("v1.12.1_d990331").
			SetEnvdVersion("0.2.4").
			SetNillableCreatedAt(build.createdAt).
			SetClusterNodeID("integration-test-node").
			Save(ctx)
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

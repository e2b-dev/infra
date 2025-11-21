package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apidb "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/testutils"
	"github.com/e2b-dev/infra/packages/db/types"
)

// createTestTeam creates a test team in the database using raw SQL
func createTestTeam(t *testing.T, ctx context.Context, db *client.Client) uuid.UUID {
	t.Helper()
	teamID := uuid.New()

	// Insert a team directly into the database using raw SQL
	// Using the default tier 'base_v1' that is created in migrations
	err := db.TestsRawSQL(ctx,
		"INSERT INTO public.teams (id, name, tier, email) VALUES ($1, $2, $3, $4)",
		teamID, "Test Team "+teamID.String(), "base_v1", "test-"+teamID.String()+"@example.com",
	)
	require.NoError(t, err, "Failed to create test team")

	return teamID
}

// createTestBaseEnv creates a base env in the database (required by foreign key constraint)
func createTestBaseEnv(t *testing.T, ctx context.Context, db *client.Client, teamID uuid.UUID) string {
	t.Helper()
	envID := "base-env-" + uuid.New().String()

	// Insert a base env directly into the database
	err := db.TestsRawSQL(ctx,
		"INSERT INTO public.envs (id, team_id, public, updated_at) VALUES ($1, $2, $3, NOW())",
		envID, teamID, true,
	)
	require.NoError(t, err, "Failed to create test base env")

	return envID
}

// createTestSnapshot creates a snapshot using UpsertSnapshot and returns the sandbox_id and env_id
func createTestSnapshot(t *testing.T, ctx context.Context, db *client.Client, teamID uuid.UUID, baseEnvID string) (string, string) {
	t.Helper()

	sandboxID := "sandbox-" + uuid.New().String()
	envID := "env-" + uuid.New().String()
	kernelVersion := "6.1.0"
	firecrackerVersion := "1.4.0"
	envdVersion := "v1.0.0"
	totalDiskSize := int64(1024)
	allowInternet := true

	// UpsertSnapshot will create the env automatically, so we pass envID as TemplateID
	params := queries.UpsertSnapshotParams{
		TemplateID:          envID,
		TeamID:              teamID,
		SandboxID:           sandboxID,
		BaseTemplateID:      baseEnvID,
		StartedAt:           pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Vcpu:                2,
		RamMb:               2048,
		TotalDiskSizeMb:     &totalDiskSize,
		Metadata:            types.JSONBStringMap{"key": "value", "type": "test"},
		KernelVersion:       kernelVersion,
		FirecrackerVersion:  firecrackerVersion,
		EnvdVersion:         &envdVersion,
		Secure:              true,
		AllowInternetAccess: &allowInternet,
		AutoPause:           true,
		Config: &types.PausedSandboxConfig{
			Version: types.PausedSandboxConfigVersion,
			Network: &types.SandboxNetworkConfig{
				Egress: &types.SandboxNetworkEgressConfig{
					AllowedAddresses: []string{"192.168.1.0/24"},
				},
			},
		},
		OriginNodeID: "node-1",
		Status:       "success",
	}

	result, err := db.UpsertSnapshot(ctx, params)
	require.NoError(t, err, "Failed to create test snapshot")
	require.NotEqual(t, uuid.Nil, result.BuildID, "BuildID should not be nil")

	// Return sandboxID and the env_id from the result (which is returned as TemplateID)
	return sandboxID, result.TemplateID
}

// createTestEnvBuild creates an env build for testing
func createTestEnvBuild(t *testing.T, ctx context.Context, db *client.Client, envID string) uuid.UUID {
	t.Helper()

	buildID := uuid.New()
	vcpu := int32(2)
	ramMb := int32(2048)
	freeDisk := int64(512)
	totalDisk := int64(1024)

	err := db.TestsRawSQL(ctx,
		`INSERT INTO public.env_builds
		(id, env_id, status, vcpu, ram_mb, free_disk_size_mb, total_disk_size_mb, kernel_version, firecracker_version, envd_version, cluster_node_id, created_at, updated_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW(), 1)`,
		buildID, envID, "ready", vcpu, ramMb, freeDisk, totalDisk, "6.1.0", "1.4.0", "v1.0.0", "test-node",
	)
	require.NoError(t, err, "Failed to create test env build")

	return buildID
}

func TestGetSnapshotWithBuilds_Success(t *testing.T) {
	// Setup test database with migrations
	db := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create test data: team -> base env -> snapshot with env -> env builds
	teamID := createTestTeam(t, ctx, db)
	baseEnvID := createTestBaseEnv(t, ctx, db, teamID)
	sandboxID, envID := createTestSnapshot(t, ctx, db, teamID, baseEnvID)

	// Create additional builds for the env (UpsertSnapshot already created one)
	buildID1 := createTestEnvBuild(t, ctx, db, envID)
	buildID2 := createTestEnvBuild(t, ctx, db, envID)

	// Execute GetSnapshotWithBuilds
	result, err := apidb.GetSnapshotWithBuilds(ctx, db, teamID, sandboxID)
	require.NoError(t, err, "GetSnapshotWithBuilds should succeed")

	// Verify snapshot data
	assert.Equal(t, sandboxID, result.SandboxID, "SandboxID should match")
	assert.Equal(t, teamID, result.TeamID, "TeamID should match")
	assert.Equal(t, envID, result.EnvID, "EnvID should match")
	assert.Equal(t, baseEnvID, result.BaseEnvID, "BaseEnvID should match")
	assert.NotNil(t, result.Metadata, "Metadata should not be nil")
	assert.Equal(t, "value", result.Metadata["key"], "Metadata should contain expected values")

	// Verify builds are returned (1 from UpsertSnapshot + 2 additional = 3 total)
	assert.Len(t, result.Builds, 3, "Should have 3 builds total")

	// Verify the additional build IDs are present
	buildIDs := make(map[uuid.UUID]bool)
	for _, build := range result.Builds {
		buildIDs[build.ID] = true
	}
	assert.True(t, buildIDs[buildID1], "Should contain first additional build ID")
	assert.True(t, buildIDs[buildID2], "Should contain second additional build ID")

	// Verify build data is populated (checking one of our created builds)
	var testBuild *queries.EnvBuild
	for i := range result.Builds {
		if result.Builds[i].ID == buildID1 {
			testBuild = &result.Builds[i]
			break
		}
	}
	require.NotNil(t, testBuild, "Should find our test build")
	assert.Equal(t, envID, testBuild.EnvID, "Build EnvID should match snapshot EnvID")
	assert.Equal(t, "ready", testBuild.Status, "Build status should match")
	assert.Equal(t, int64(2), testBuild.Vcpu, "Build vCPU should match")
	assert.Equal(t, int64(2048), testBuild.RamMb, "Build RAM should match")
}

func TestGetSnapshotWithBuilds_NoAdditionalBuilds(t *testing.T) {
	// Setup test database with migrations
	db := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create test data: team -> base env -> snapshot
	// Note: UpsertSnapshot creates one build automatically
	teamID := createTestTeam(t, ctx, db)
	baseEnvID := createTestBaseEnv(t, ctx, db, teamID)
	sandboxID, envID := createTestSnapshot(t, ctx, db, teamID, baseEnvID)

	// Execute GetSnapshotWithBuilds (only the build created by UpsertSnapshot)
	result, err := apidb.GetSnapshotWithBuilds(ctx, db, teamID, sandboxID)
	require.NoError(t, err, "GetSnapshotWithBuilds should succeed")

	// Verify snapshot data
	assert.Equal(t, sandboxID, result.SandboxID, "SandboxID should match")
	assert.Equal(t, teamID, result.TeamID, "TeamID should match")
	assert.Equal(t, envID, result.EnvID, "EnvID should match")

	// Verify the build created by UpsertSnapshot is returned
	assert.Len(t, result.Builds, 1, "Should have exactly 1 build (created by UpsertSnapshot)")
	assert.NotEqual(t, uuid.Nil, result.Builds[0].ID, "Build ID should not be nil")
	assert.Equal(t, envID, result.Builds[0].EnvID, "Build EnvID should match snapshot EnvID")
	assert.Equal(t, "success", result.Builds[0].Status, "Build status should match")
}

func TestGetSnapshotWithBuilds_NotFound(t *testing.T) {
	// Setup test database with migrations
	db := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create a team but no snapshot
	teamID := createTestTeam(t, ctx, db)
	nonExistentSandboxID := "sandbox-does-not-exist"

	// Execute GetSnapshotWithBuilds with non-existent sandbox ID
	result, err := apidb.GetSnapshotWithBuilds(ctx, db, teamID, nonExistentSandboxID)

	// Verify error is returned
	require.Error(t, err, "GetSnapshotWithBuilds should return an error")
	assert.ErrorIs(t, err, apidb.ErrSnapshotNotFound, "Error should be ErrSnapshotNotFound")

	// Verify result is empty
	assert.Equal(t, "", result.SandboxID, "Result should be empty on error")
	assert.Nil(t, result.Builds, "Builds should be nil on error")
}

func TestGetSnapshotWithBuilds_WrongTeamID(t *testing.T) {
	// Setup test database with migrations
	db := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create test data with one team
	teamID := createTestTeam(t, ctx, db)
	baseEnvID := createTestBaseEnv(t, ctx, db, teamID)
	sandboxID, _ := createTestSnapshot(t, ctx, db, teamID, baseEnvID)

	// Create a different team
	differentTeamID := createTestTeam(t, ctx, db)

	// Execute GetSnapshotWithBuilds with wrong team ID
	result, err := apidb.GetSnapshotWithBuilds(ctx, db, differentTeamID, sandboxID)

	// Verify error is returned (snapshot exists but doesn't belong to this team)
	require.Error(t, err, "GetSnapshotWithBuilds should return an error for wrong team")
	assert.ErrorIs(t, err, apidb.ErrSnapshotNotFound, "Error should be ErrSnapshotNotFound")

	// Verify result is empty
	assert.Equal(t, "", result.SandboxID, "Result should be empty on error")
	assert.Nil(t, result.Builds, "Builds should be nil on error")
}

func TestGetSnapshotWithBuilds_MultipleBuildsVerification(t *testing.T) {
	// Setup test database with migrations
	db := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create test data
	teamID := createTestTeam(t, ctx, db)
	baseEnvID := createTestBaseEnv(t, ctx, db, teamID)
	sandboxID, envID := createTestSnapshot(t, ctx, db, teamID, baseEnvID)

	// Create 3 additional builds (UpsertSnapshot already created 1, so 4 total)
	buildID1 := createTestEnvBuild(t, ctx, db, envID)
	buildID2 := createTestEnvBuild(t, ctx, db, envID)
	buildID3 := createTestEnvBuild(t, ctx, db, envID)

	// Execute GetSnapshotWithBuilds
	result, err := apidb.GetSnapshotWithBuilds(ctx, db, teamID, sandboxID)
	require.NoError(t, err, "GetSnapshotWithBuilds should succeed")

	// Verify we got all 4 builds (1 from UpsertSnapshot + 3 additional)
	assert.Len(t, result.Builds, 4, "Should have 4 builds total")

	// Verify all build IDs are present and unique
	buildIDs := make(map[uuid.UUID]bool)
	for _, build := range result.Builds {
		assert.NotEqual(t, uuid.Nil, build.ID, "Build ID should not be nil")
		assert.Equal(t, envID, build.EnvID, "All builds should have the same EnvID")
		buildIDs[build.ID] = true
	}

	assert.Len(t, buildIDs, 4, "All build IDs should be unique")
	assert.True(t, buildIDs[buildID1], "Should contain first additional build ID")
	assert.True(t, buildIDs[buildID2], "Should contain second additional build ID")
	assert.True(t, buildIDs[buildID3], "Should contain third additional build ID")
}

package snapshots

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	// After the env_builds migration, envs table only has: id, team_id, public, updated_at, build_count, spawn_count, last_spawned_at
	err := db.TestsRawSQL(ctx,
		"INSERT INTO public.envs (id, team_id, public, updated_at) VALUES ($1, $2, $3, NOW())",
		envID, teamID, true,
	)
	require.NoError(t, err, "Failed to create test base env")

	return envID
}

// getSnapshotMetadata retrieves the metadata from a snapshot using raw SQL
func getSnapshotMetadata(t *testing.T, ctx context.Context, db *client.Client, sandboxID string) types.JSONBStringMap {
	t.Helper()
	var metadata types.JSONBStringMap

	err := db.TestsRawSQLQuery(ctx,
		"SELECT metadata FROM public.snapshots WHERE sandbox_id = $1",
		func(rows pgx.Rows) error {
			if !rows.Next() {
				return nil
			}

			return rows.Scan(&metadata)
		},
		sandboxID,
	)
	require.NoError(t, err, "Failed to query snapshot metadata")

	return metadata
}

// BuildAssignment represents a row from env_build_assignments
type BuildAssignment struct {
	ID      uuid.UUID
	EnvID   string
	BuildID uuid.UUID
	Tag     string
	Source  string
}

// getBuildAssignments retrieves all build assignments for a given env_id
func getBuildAssignments(t *testing.T, ctx context.Context, db *client.Client, envID string) []BuildAssignment {
	t.Helper()
	var assignments []BuildAssignment

	err := db.TestsRawSQLQuery(ctx,
		"SELECT id, env_id, build_id, tag, source FROM public.env_build_assignments WHERE env_id = $1 ORDER BY created_at DESC",
		func(rows pgx.Rows) error {
			for rows.Next() {
				var a BuildAssignment
				if err := rows.Scan(&a.ID, &a.EnvID, &a.BuildID, &a.Tag, &a.Source); err != nil {
					return err
				}
				assignments = append(assignments, a)
			}

			return nil
		},
		envID,
	)
	require.NoError(t, err, "Failed to query build assignments")

	return assignments
}

// getBuildAssignmentByBuildID retrieves a build assignment for a specific build_id
func getBuildAssignmentByBuildID(t *testing.T, ctx context.Context, db *client.Client, buildID uuid.UUID) *BuildAssignment {
	t.Helper()
	var assignment *BuildAssignment

	err := db.TestsRawSQLQuery(ctx,
		"SELECT id, env_id, build_id, tag, source FROM public.env_build_assignments WHERE build_id = $1",
		func(rows pgx.Rows) error {
			if rows.Next() {
				assignment = &BuildAssignment{}

				return rows.Scan(&assignment.ID, &assignment.EnvID, &assignment.BuildID, &assignment.Tag, &assignment.Source)
			}

			return nil
		},
		buildID,
	)
	require.NoError(t, err, "Failed to query build assignment by build_id")

	return assignment
}

// getEnvByID retrieves an env by ID to verify it exists
func getEnvByID(t *testing.T, ctx context.Context, db *client.Client, envID string) bool {
	t.Helper()
	var exists bool

	err := db.TestsRawSQLQuery(ctx,
		"SELECT EXISTS(SELECT 1 FROM public.envs WHERE id = $1)",
		func(rows pgx.Rows) error {
			if rows.Next() {
				return rows.Scan(&exists)
			}

			return nil
		},
		envID,
	)
	require.NoError(t, err, "Failed to check if env exists")

	return exists
}

// getEnvBuildByID retrieves an env_build by ID to verify it exists
func getEnvBuildByID(t *testing.T, ctx context.Context, db *client.Client, buildID uuid.UUID) bool {
	t.Helper()
	var exists bool

	err := db.TestsRawSQLQuery(ctx,
		"SELECT EXISTS(SELECT 1 FROM public.env_builds WHERE id = $1)",
		func(rows pgx.Rows) error {
			if rows.Next() {
				return rows.Scan(&exists)
			}

			return nil
		},
		buildID,
	)
	require.NoError(t, err, "Failed to check if env_build exists")

	return exists
}

func TestUpsertSnapshot_NewSnapshot(t *testing.T) {
	t.Parallel()
	// Setup test database with migrations
	client := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create a test team first (required by foreign key constraint)
	teamID := createTestTeam(t, ctx, client)
	// Create a base env (required by foreign key constraint on snapshots table)
	baseTemplateID := createTestBaseEnv(t, ctx, client, teamID)

	// Prepare test data for a new snapshot
	templateID := "test-template-" + uuid.New().String()
	sandboxID := "sandbox-" + uuid.New().String()
	originNodeID := "node-1"
	envdVersion := "v1.0.0"
	kernelVersion := "6.1.0"
	firecrackerVersion := "1.4.0"
	totalDiskSize := int64(1024)
	allowInternet := true

	params := queries.UpsertSnapshotParams{
		TemplateID:          templateID,
		TeamID:              teamID,
		SandboxID:           sandboxID,
		BaseTemplateID:      baseTemplateID,
		StartedAt:           pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Vcpu:                2,
		RamMb:               2048,
		TotalDiskSizeMb:     &totalDiskSize,
		Metadata:            types.JSONBStringMap{"key": "value"},
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
		OriginNodeID: originNodeID,
		Status:       "snapshotting",
	}

	// Execute UpsertSnapshot for a new snapshot
	result, err := client.UpsertSnapshot(ctx, params)
	require.NoError(t, err, "Failed to create new snapshot")

	// Verify the result
	assert.NotEqual(t, uuid.Nil, result.BuildID, "BuildID should not be nil")
	assert.Equal(t, templateID, result.TemplateID, "TemplateID should match the input for a new snapshot")

	// Verify we can retrieve the snapshot using the sandboxID
	// Note: GetLastSnapshot requires status='success', so we can't use it here
	// Instead, we verify the returned IDs are valid
	assert.NotEmpty(t, result.TemplateID, "TemplateID should not be empty")
	assert.NotEqual(t, uuid.Nil, result.BuildID, "BuildID should not be nil")

	// Verify metadata was stored correctly
	storedMetadata := getSnapshotMetadata(t, ctx, client, sandboxID)
	assert.Equal(t, params.Metadata, storedMetadata, "Stored metadata should match the input metadata")
	assert.Equal(t, "value", storedMetadata["key"], "Metadata key 'key' should have value 'value'")

	// Verify envs entry was created
	envExists := getEnvByID(t, ctx, client, result.TemplateID)
	assert.True(t, envExists, "Env entry should exist in envs table")

	// Verify env_builds entry was created
	buildExists := getEnvBuildByID(t, ctx, client, result.BuildID)
	assert.True(t, buildExists, "Build entry should exist in env_builds table")

	// Verify env_build_assignments entry was created
	assignment := getBuildAssignmentByBuildID(t, ctx, client, result.BuildID)
	require.NotNil(t, assignment, "Build assignment should exist for the new build")
	assert.Equal(t, result.TemplateID, assignment.EnvID, "Assignment env_id should match template_id")
	assert.Equal(t, result.BuildID, assignment.BuildID, "Assignment build_id should match")
	assert.Equal(t, "default", assignment.Tag, "Assignment tag should be 'default'")
}

func TestUpsertSnapshot_ExistingSnapshot(t *testing.T) {
	t.Parallel()
	// Setup test database with migrations
	client := testutils.SetupDatabase(t)
	ctx := context.Background()

	// Create a test team first (required by foreign key constraint)
	teamID := createTestTeam(t, ctx, client)
	// Create a base env (required by foreign key constraint on snapshots table)
	baseTemplateID := createTestBaseEnv(t, ctx, client, teamID)

	// Prepare test data for the first snapshot creation
	templateID := "test-template-" + uuid.New().String()
	sandboxID := "sandbox-" + uuid.New().String()
	originNodeID := "node-1"
	envdVersion := "v1.0.0"
	kernelVersion := "6.1.0"
	firecrackerVersion := "1.4.0"
	totalDiskSize := int64(1024)
	allowInternet := true

	initialParams := queries.UpsertSnapshotParams{
		TemplateID:          templateID,
		TeamID:              teamID,
		SandboxID:           sandboxID,
		BaseTemplateID:      baseTemplateID,
		StartedAt:           pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Hour), Valid: true},
		Vcpu:                2,
		RamMb:               2048,
		TotalDiskSizeMb:     &totalDiskSize,
		Metadata:            types.JSONBStringMap{"key": "initial_value"},
		KernelVersion:       kernelVersion,
		FirecrackerVersion:  firecrackerVersion,
		EnvdVersion:         &envdVersion,
		Secure:              true,
		AllowInternetAccess: &allowInternet,
		AutoPause:           false,
		Config: &types.PausedSandboxConfig{
			Version: types.PausedSandboxConfigVersion,
		},
		OriginNodeID: originNodeID,
		Status:       "snapshotting",
	}

	// Create the initial snapshot
	initialResult, err := client.UpsertSnapshot(ctx, initialParams)
	require.NoError(t, err, "Failed to create initial snapshot")
	initialBuildID := initialResult.BuildID
	initialTemplateID := initialResult.TemplateID

	// Verify initial results
	assert.NotEqual(t, uuid.Nil, initialBuildID, "Initial BuildID should not be nil")
	assert.Equal(t, templateID, initialTemplateID, "Initial TemplateID should match input")

	// Verify initial metadata was stored correctly
	initialStoredMetadata := getSnapshotMetadata(t, ctx, client, sandboxID)
	assert.Equal(t, initialParams.Metadata, initialStoredMetadata, "Initial metadata should match")
	assert.Equal(t, "initial_value", initialStoredMetadata["key"], "Initial metadata key should have correct value")

	// Verify initial envs, env_builds, and build assignment were created
	assert.True(t, getEnvByID(t, ctx, client, initialTemplateID), "Initial env should exist")
	assert.True(t, getEnvBuildByID(t, ctx, client, initialBuildID), "Initial build should exist")
	initialAssignment := getBuildAssignmentByBuildID(t, ctx, client, initialBuildID)
	require.NotNil(t, initialAssignment, "Initial build assignment should exist")
	assert.Equal(t, initialTemplateID, initialAssignment.EnvID, "Initial assignment env_id should match")
	assert.Equal(t, "default", initialAssignment.Tag, "Initial assignment tag should be 'default'")

	// Prepare updated data for the existing snapshot (same sandbox_id)
	updatedOriginNodeID := "node-2"
	newStartTime := time.Now()
	updatedMetadata := types.JSONBStringMap{"key": "updated_value", "new_key": "new_value"}
	updatedConfig := &types.PausedSandboxConfig{
		Version: types.PausedSandboxConfigVersion,
		Network: &types.SandboxNetworkConfig{
			Egress: &types.SandboxNetworkEgressConfig{
				AllowedAddresses: []string{"10.0.0.0/8"},
			},
		},
	}

	updatedParams := queries.UpsertSnapshotParams{
		TemplateID:          "new-template-id-should-be-ignored", // This should be ignored for existing snapshots
		TeamID:              teamID,
		SandboxID:           sandboxID, // Same sandbox_id as initial - this is the key
		BaseTemplateID:      baseTemplateID,
		StartedAt:           pgtype.Timestamptz{Time: newStartTime, Valid: true},
		Vcpu:                4,    // Updated from 2
		RamMb:               4096, // Updated from 2048
		TotalDiskSizeMb:     &totalDiskSize,
		Metadata:            updatedMetadata, // Updated metadata
		KernelVersion:       kernelVersion,
		FirecrackerVersion:  firecrackerVersion,
		EnvdVersion:         &envdVersion,
		Secure:              true,
		AllowInternetAccess: &allowInternet,
		AutoPause:           true,                // Updated from false
		Config:              updatedConfig,       // Updated config
		OriginNodeID:        updatedOriginNodeID, // Updated from node-1
		Status:              "snapshotting",
	}

	// Execute UpsertSnapshot for the existing snapshot (same sandbox_id)
	updatedResult, err := client.UpsertSnapshot(ctx, updatedParams)
	require.NoError(t, err, "Failed to update existing snapshot")

	// Verify the key behavior of upserting an existing snapshot:
	// 1. TemplateID should remain the same (from the first insert)
	assert.Equal(t, initialTemplateID, updatedResult.TemplateID,
		"TemplateID should remain the same as the initial snapshot when upserting")

	// 2. BuildID should be different (a new build is created each time)
	assert.NotEqual(t, initialBuildID, updatedResult.BuildID,
		"BuildID should be different - a new build is created on each upsert")

	// 3. The new BuildID should be valid
	assert.NotEqual(t, uuid.Nil, updatedResult.BuildID,
		"New BuildID should not be nil")

	// 4. Verify metadata was updated correctly
	updatedStoredMetadata := getSnapshotMetadata(t, ctx, client, sandboxID)
	assert.Equal(t, updatedParams.Metadata, updatedStoredMetadata, "Updated metadata should match")
	assert.Equal(t, "updated_value", updatedStoredMetadata["key"], "Updated metadata key should have new value")
	assert.Equal(t, "new_value", updatedStoredMetadata["new_key"], "New metadata key should be present")
	assert.NotEqual(t, initialStoredMetadata, updatedStoredMetadata, "Updated metadata should be different from initial")

	// 5. Verify the second build and its assignment were created
	assert.True(t, getEnvBuildByID(t, ctx, client, updatedResult.BuildID), "Second build should exist")
	secondAssignment := getBuildAssignmentByBuildID(t, ctx, client, updatedResult.BuildID)
	require.NotNil(t, secondAssignment, "Second build assignment should exist")
	assert.Equal(t, initialTemplateID, secondAssignment.EnvID, "Second assignment env_id should match original template")
	assert.Equal(t, "default", secondAssignment.Tag, "Second assignment tag should be 'default'")

	// 6. Verify we now have 2 build assignments for the same env_id
	allAssignments := getBuildAssignments(t, ctx, client, initialTemplateID)
	assert.GreaterOrEqual(t, len(allAssignments), 2, "Should have at least 2 build assignments after second upsert")

	// Test calling upsert a third time to ensure consistent behavior
	thirdParams := queries.UpsertSnapshotParams{
		TemplateID:          "yet-another-template-id",
		TeamID:              teamID,
		SandboxID:           sandboxID, // Same sandbox_id again
		BaseTemplateID:      baseTemplateID,
		StartedAt:           pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Vcpu:                8,
		RamMb:               8192,
		TotalDiskSizeMb:     &totalDiskSize,
		Metadata:            types.JSONBStringMap{"key": "third_value"},
		KernelVersion:       kernelVersion,
		FirecrackerVersion:  firecrackerVersion,
		EnvdVersion:         &envdVersion,
		Secure:              true,
		AllowInternetAccess: &allowInternet,
		AutoPause:           true,
		Config:              updatedConfig,
		OriginNodeID:        "node-3",
		Status:              "snapshotting",
	}

	thirdResult, err := client.UpsertSnapshot(ctx, thirdParams)
	require.NoError(t, err, "Failed to update snapshot a third time")

	// Verify consistent behavior on third upsert
	assert.Equal(t, initialTemplateID, thirdResult.TemplateID,
		"TemplateID should still be the same as the initial snapshot")
	assert.NotEqual(t, updatedResult.BuildID, thirdResult.BuildID,
		"BuildID should be different from the second upsert")
	assert.NotEqual(t, initialBuildID, thirdResult.BuildID,
		"BuildID should be different from the first upsert")

	// Verify metadata was updated again
	thirdStoredMetadata := getSnapshotMetadata(t, ctx, client, sandboxID)
	assert.Equal(t, thirdParams.Metadata, thirdStoredMetadata, "Third metadata should match")
	assert.Equal(t, "third_value", thirdStoredMetadata["key"], "Third metadata key should have latest value")
	assert.NotEqual(t, updatedStoredMetadata, thirdStoredMetadata, "Third metadata should be different from second")

	// Verify the third build and its assignment were created
	assert.True(t, getEnvBuildByID(t, ctx, client, thirdResult.BuildID), "Third build should exist")
	thirdAssignment := getBuildAssignmentByBuildID(t, ctx, client, thirdResult.BuildID)
	require.NotNil(t, thirdAssignment, "Third build assignment should exist")
	assert.Equal(t, initialTemplateID, thirdAssignment.EnvID, "Third assignment env_id should match original template")
	assert.Equal(t, "default", thirdAssignment.Tag, "Third assignment tag should be 'default'")

	// Verify we now have 3 build assignments for the same env_id
	finalAssignments := getBuildAssignments(t, ctx, client, initialTemplateID)
	assert.GreaterOrEqual(t, len(finalAssignments), 3, "Should have at least 3 build assignments after third upsert")
}

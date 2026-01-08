package snapshots

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/testutils"
	"github.com/e2b-dev/infra/packages/db/types"
)

// TestGetLastSnapshot_ReturnsLatestAssignment verifies that GetLastSnapshot returns
// the build from the most recent assignment (ordered by assignment created_at DESC).
func TestGetLastSnapshot_ReturnsLatestAssignment(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create team and base template for snapshot
	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	// Create first snapshot (creates template, build, and assignment)
	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	result1 := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build1ID := result1.BuildID

	// Small delay to ensure different created_at timestamps
	time.Sleep(10 * time.Millisecond)

	// Upsert again with same sandbox_id - creates a new build and assignment
	result2 := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build2ID := result2.BuildID

	// Verify we got different builds
	require.NotEqual(t, build1ID, build2ID, "Each upsert should create a new build")

	// Execute GetLastSnapshot - should return the latest build (build2)
	snapshot, err := db.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, build2ID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should return the build from the latest assignment")
}

// TestGetLastSnapshot_ReturnsLatestAfterMultipleUpserts verifies that after multiple
// upserts, GetLastSnapshot always returns the most recent build.
func TestGetLastSnapshot_ReturnsLatestAfterMultipleUpserts(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create team and base template
	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	// Create multiple snapshots (each upsert creates a new build)
	var lastBuildID uuid.UUID
	for i := 0; i < 3; i++ {
		time.Sleep(10 * time.Millisecond)
		result := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
		lastBuildID = result.BuildID
	}

	// GetLastSnapshot should return the most recent build
	snapshot, err := db.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, lastBuildID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should return the most recent build after multiple upserts")
}

// TestGetLastSnapshot_OnlyReturnsSuccessBuilds verifies that GetLastSnapshot only
// returns builds with status='success'.
func TestGetLastSnapshot_OnlyReturnsSuccessBuilds(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create team and base template
	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	// Create first snapshot with success status
	result1 := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	successBuildID := result1.BuildID

	time.Sleep(10 * time.Millisecond)

	// Create second snapshot with snapshotting status (not success)
	upsertTestSnapshotWithStatus(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID, "snapshotting")

	// GetLastSnapshot should return the success build, not the snapshotting one
	snapshot, err := db.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, successBuildID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should only return builds with status='success'")
}

// TestGetSnapshotsWithCursor_ReturnsLatestAssignment verifies that GetSnapshotsWithCursor
// returns the build from the most recent assignment for each snapshot.
func TestGetSnapshotsWithCursor_ReturnsLatestAssignment(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create team and base template
	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	// Create first snapshot
	upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	time.Sleep(10 * time.Millisecond)

	// Upsert again - creates a new build (latest)
	result2 := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	latestBuildID := result2.BuildID

	// Execute GetSnapshotsWithCursor
	results, err := db.GetSnapshotsWithCursor(ctx, queries.GetSnapshotsWithCursorParams{
		TeamID:                teamID,
		Metadata:              types.JSONBStringMap{},
		CursorID:              "",
		CursorTime:            pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		SnapshotExcludeSbxIds: []string{},
		Limit:                 10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1, "Should return 1 snapshot")

	assert.Equal(t, latestBuildID, results[0].EnvBuild.ID,
		"GetSnapshotsWithCursor should return the build from the latest assignment")
}

// TestGetLastSnapshot_BuildSharedWithOtherTemplate verifies that when a build is assigned
// to multiple templates, GetLastSnapshot still returns the correct build for this template.
func TestGetLastSnapshot_BuildSharedWithOtherTemplate(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create team and base template
	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	// Create snapshot with first build
	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	result1 := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build1ID := result1.BuildID

	time.Sleep(10 * time.Millisecond)

	// Create second build (latest for this snapshot)
	result2 := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build2ID := result2.BuildID

	// Now assign build1 to a DIFFERENT template (simulating shared build)
	otherTemplateID := testutils.CreateTestTemplate(t, db, teamID)
	time.Sleep(10 * time.Millisecond)
	testutils.CreateTestBuildAssignment(t, ctx, db, otherTemplateID, build1ID, "default")

	// GetLastSnapshot should still return build2 (latest for THIS template),
	// not build1 even though build1 has a newer assignment to another template
	snapshot, err := db.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, build2ID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should return latest build for THIS template, ignoring assignments to other templates")
}

// TestGetLastSnapshot_IgnoresNonDefaultTags verifies that assignments with non-default
// tags are filtered out, even if they are more recent.
func TestGetLastSnapshot_IgnoresNonDefaultTags(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create team and base template
	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	// Create snapshot (creates build with 'default' tag)
	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	result1 := upsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	defaultBuildID := result1.BuildID

	time.Sleep(10 * time.Millisecond)

	// Create another build and assign it with a non-default tag (e.g., "v1")
	// This should be ignored by GetLastSnapshot
	otherBuildID := testutils.CreateTestBuild(t, ctx, db, result1.TemplateID, "success")
	testutils.CreateTestBuildAssignment(t, ctx, db, result1.TemplateID, otherBuildID, "v1")

	// GetLastSnapshot should return the default-tagged build, not the v1-tagged one
	snapshot, err := db.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, defaultBuildID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should only consider 'default' tag assignments")
}

// TestGetLastSnapshot_AssignmentOrderDifferentFromBuildOrder verifies that when
// assignment created_at order differs from build created_at order, the assignment
// order takes precedence.
func TestGetLastSnapshot_AssignmentOrderDifferentFromBuildOrder(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	// Create team and templates
	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)
	snapshotTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	// Create builds: build1 (older), build2 (newer)
	build1ID := testutils.CreateTestBuild(t, ctx, db, snapshotTemplateID, "success")
	time.Sleep(10 * time.Millisecond)
	build2ID := testutils.CreateTestBuild(t, ctx, db, snapshotTemplateID, "success")

	// Assign in REVERSE order: build2 first, then build1
	// Even though build2 was created later, build1 has the latest assignment
	testutils.CreateTestBuildAssignment(t, ctx, db, snapshotTemplateID, build2ID, "default")
	time.Sleep(10 * time.Millisecond)
	testutils.CreateTestBuildAssignment(t, ctx, db, snapshotTemplateID, build1ID, "default")

	// Create snapshot record
	sandboxID := "sandbox-" + uuid.New().String()
	createSnapshotRecord(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	// GetLastSnapshot should return build1 (latest assignment), not build2 (latest build)
	snapshot, err := db.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, build1ID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should use assignment order, not build creation order")
}

// createSnapshotRecord creates just the snapshot record without creating a new build
func createSnapshotRecord(t *testing.T, ctx context.Context, db *client.Client, templateID, sandboxID string, teamID uuid.UUID, baseTemplateID string) {
	t.Helper()

	err := db.TestsRawSQL(ctx,
		`INSERT INTO public.snapshots 
		(sandbox_id, env_id, team_id, base_env_id, sandbox_started_at, metadata)
		VALUES ($1, $2, $3, $4, NOW(), '{}'::jsonb)`,
		sandboxID, templateID, teamID, baseTemplateID,
	)
	require.NoError(t, err, "Failed to create snapshot record")
}

// upsertTestSnapshot creates/updates a snapshot for testing with success status
func upsertTestSnapshot(t *testing.T, ctx context.Context, db *client.Client, templateID, sandboxID string, teamID uuid.UUID, baseTemplateID string) queries.UpsertSnapshotRow {
	t.Helper()

	return upsertTestSnapshotWithStatus(t, ctx, db, templateID, sandboxID, teamID, baseTemplateID, "success")
}

// upsertTestSnapshotWithStatus creates/updates a snapshot with a specific status
func upsertTestSnapshotWithStatus(t *testing.T, ctx context.Context, db *client.Client, templateID, sandboxID string, teamID uuid.UUID, baseTemplateID string, status string) queries.UpsertSnapshotRow {
	t.Helper()

	totalDiskSize := int64(1024)
	envdVersion := "v1.0.0"
	allowInternet := true

	result, err := db.UpsertSnapshot(ctx, queries.UpsertSnapshotParams{
		TemplateID:          templateID,
		TeamID:              teamID,
		SandboxID:           sandboxID,
		BaseTemplateID:      baseTemplateID,
		StartedAt:           pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Vcpu:                2,
		RamMb:               2048,
		TotalDiskSizeMb:     &totalDiskSize,
		Metadata:            types.JSONBStringMap{},
		KernelVersion:       "6.1.0",
		FirecrackerVersion:  "1.4.0",
		EnvdVersion:         &envdVersion,
		Secure:              true,
		AllowInternetAccess: &allowInternet,
		AutoPause:           true,
		OriginNodeID:        "test-node",
		Status:              status,
	})
	require.NoError(t, err, "Failed to upsert test snapshot")

	return result
}

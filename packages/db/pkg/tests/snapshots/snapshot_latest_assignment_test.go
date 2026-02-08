package snapshots

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
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

	result1 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build1ID := result1.BuildID

	// Small delay to ensure different created_at timestamps
	time.Sleep(10 * time.Millisecond)

	// Upsert again with same sandbox_id - creates a new build and assignment
	result2 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build2ID := result2.BuildID

	// Verify we got different builds
	require.NotEqual(t, build1ID, build2ID, "Each upsert should create a new build")

	// Execute GetLastSnapshot - should return the latest build (build2)
	snapshot, err := db.SqlcClient.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, build2ID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should return the build from the latest assignment")
}

// TestGetLastSnapshot_OnlyReturnsSuccessBuilds verifies that GetLastSnapshot only
// returns builds with status IN ('success', 'uploaded').
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
	result1 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	successBuildID := result1.BuildID

	time.Sleep(10 * time.Millisecond)

	// Create second snapshot with snapshotting status (not success)
	testutils.UpsertTestSnapshotWithStatus(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID, types.BuildStatusSnapshotting)

	// GetLastSnapshot should return the success build, not the snapshotting one
	snapshot, err := db.SqlcClient.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, successBuildID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should only return builds with status IN ('success', 'uploaded')")
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
	testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	time.Sleep(10 * time.Millisecond)

	// Upsert again - creates a new build (latest)
	result2 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	latestBuildID := result2.BuildID

	// Execute GetSnapshotsWithCursor
	results, err := db.SqlcClient.GetSnapshotsWithCursor(ctx, queries.GetSnapshotsWithCursorParams{
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
	result1 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build1ID := result1.BuildID

	time.Sleep(10 * time.Millisecond)

	// Create second build (latest for this snapshot)
	result2 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	build2ID := result2.BuildID

	// Now assign build1 to a DIFFERENT template (simulating shared build)
	otherTemplateID := testutils.CreateTestTemplate(t, db, teamID)
	time.Sleep(10 * time.Millisecond)
	testutils.CreateTestBuildAssignment(t, ctx, db, otherTemplateID, build1ID, "default")

	// GetLastSnapshot should still return build2 (latest for THIS template),
	// not build1 even though build1 has a newer assignment to another template
	snapshot, err := db.SqlcClient.GetLastSnapshot(ctx, sandboxID)
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
	result1 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	defaultBuildID := result1.BuildID

	time.Sleep(10 * time.Millisecond)

	// Create another build and assign it with a non-default tag (e.g., "v1")
	// This should be ignored by GetLastSnapshot
	otherBuildID := testutils.CreateTestBuild(t, ctx, db, result1.TemplateID, "success")
	// Delete the auto-created 'default' assignment from the trigger so the build only has 'v1' tag
	testutils.DeleteTriggerBuildAssignment(t, ctx, db, result1.TemplateID, otherBuildID, "default")
	testutils.CreateTestBuildAssignment(t, ctx, db, result1.TemplateID, otherBuildID, "v1")

	// GetLastSnapshot should return the default-tagged build, not the v1-tagged one
	snapshot, err := db.SqlcClient.GetLastSnapshot(ctx, sandboxID)
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
	testutils.CreateSnapshotRecord(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	// GetLastSnapshot should return build1 (latest assignment), not build2 (latest build)
	snapshot, err := db.SqlcClient.GetLastSnapshot(ctx, sandboxID)
	require.NoError(t, err)

	assert.Equal(t, build1ID, snapshot.EnvBuild.ID,
		"GetLastSnapshot should use assignment order, not build creation order")
}

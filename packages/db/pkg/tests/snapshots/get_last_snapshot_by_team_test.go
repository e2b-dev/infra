package snapshots

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
)

// TestGetLastSnapshotByTeam_ReturnsSnapshotForCorrectTeam verifies that
// GetLastSnapshotByTeam returns the snapshot when sandboxID and teamID both match.
func TestGetLastSnapshotByTeam_ReturnsSnapshotForCorrectTeam(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	result := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	snapshot, err := db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandboxID,
		TeamID:    teamID,
	})
	require.NoError(t, err)
	assert.Equal(t, result.BuildID, snapshot.EnvBuild.ID)
	assert.Equal(t, teamID, snapshot.Snapshot.TeamID)
	assert.Equal(t, sandboxID, snapshot.Snapshot.SandboxID)
}

// TestGetLastSnapshotByTeam_ReturnsNotFoundForWrongTeam verifies that
// GetLastSnapshotByTeam returns an error when the teamID does not match the snapshot owner.
func TestGetLastSnapshotByTeam_ReturnsNotFoundForWrongTeam(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	otherTeamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, ownerTeamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, ownerTeamID, baseTemplateID)

	_, err := db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandboxID,
		TeamID:    otherTeamID,
	})
	require.Error(t, err, "should return error when teamID does not match snapshot owner")
}

// TestGetLastSnapshotByTeam_ReturnsNotFoundForUnknownSandbox verifies that
// GetLastSnapshotByTeam returns an error when the sandboxID does not exist.
func TestGetLastSnapshotByTeam_ReturnsNotFoundForUnknownSandbox(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)

	_, err := db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: "nonexistent-sandbox-" + uuid.New().String(),
		TeamID:    teamID,
	})
	require.Error(t, err, "should return error for unknown sandboxID")
}

// TestGetLastSnapshotByTeam_ReturnsLatestBuildForTeam verifies that when multiple
// builds exist for the same sandbox, GetLastSnapshotByTeam returns the latest one.
func TestGetLastSnapshotByTeam_ReturnsLatestBuildForTeam(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)
	time.Sleep(10 * time.Millisecond)
	result2 := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	snapshot, err := db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandboxID,
		TeamID:    teamID,
	})
	require.NoError(t, err)
	assert.Equal(t, result2.BuildID, snapshot.EnvBuild.ID,
		"should return the latest build for the team")
}

// TestGetLastSnapshotByTeam_IsolatesBetweenTeams verifies that two teams can each
// have a snapshot for different sandboxes and each only sees their own.
func TestGetLastSnapshotByTeam_IsolatesBetweenTeams(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	team1ID := testutils.CreateTestTeam(t, db)
	team2ID := testutils.CreateTestTeam(t, db)
	base1 := testutils.CreateTestTemplate(t, db, team1ID)
	base2 := testutils.CreateTestTemplate(t, db, team2ID)

	sandbox1ID := "sandbox-" + uuid.New().String()
	sandbox2ID := "sandbox-" + uuid.New().String()

	result1 := testutils.UpsertTestSnapshot(t, ctx, db, "tmpl-"+uuid.New().String(), sandbox1ID, team1ID, base1)
	result2 := testutils.UpsertTestSnapshot(t, ctx, db, "tmpl-"+uuid.New().String(), sandbox2ID, team2ID, base2)

	// team1 can see sandbox1
	snap1, err := db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandbox1ID,
		TeamID:    team1ID,
	})
	require.NoError(t, err)
	assert.Equal(t, result1.BuildID, snap1.EnvBuild.ID)

	// team2 can see sandbox2
	snap2, err := db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandbox2ID,
		TeamID:    team2ID,
	})
	require.NoError(t, err)
	assert.Equal(t, result2.BuildID, snap2.EnvBuild.ID)

	// team1 cannot see sandbox2
	_, err = db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandbox2ID,
		TeamID:    team1ID,
	})
	require.Error(t, err, "team1 should not be able to see team2's sandbox")

	// team2 cannot see sandbox1
	_, err = db.SqlcClient.GetLastSnapshotByTeam(ctx, queries.GetLastSnapshotByTeamParams{
		SandboxID: sandbox1ID,
		TeamID:    team2ID,
	})
	require.Error(t, err, "team2 should not be able to see team1's sandbox")
}

package snapshots

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/db/queries"
)

// TestGetLastSnapshot_MatchingTeamReturnsSnapshot verifies that scoping the query
// to the owning team returns the snapshot's latest build.
func TestGetLastSnapshot_MatchingTeamReturnsSnapshot(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	result := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	snapshot, err := db.SqlcClient.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{
		SandboxID: sandboxID,
		TeamID:    &teamID,
	})
	require.NoError(t, err)

	assert.Equal(t, result.BuildID, snapshot.EnvBuild.ID,
		"GetLastSnapshot scoped to the owning team should return the snapshot build")
}

// TestGetLastSnapshot_WrongTeamReturnsNotFound is the core security assertion for
// ENG-3544: a team must not be able to read another team's snapshot. Scoping the
// query to a non-owning team must yield no rows.
func TestGetLastSnapshot_WrongTeamReturnsNotFound(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, ownerTeamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, ownerTeamID, baseTemplateID)

	otherTeamID := testutils.CreateTestTeam(t, db)

	_, err := db.SqlcClient.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{
		SandboxID: sandboxID,
		TeamID:    &otherTeamID,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows,
		"GetLastSnapshot scoped to a non-owning team must not return the snapshot")
}

// TestGetLastSnapshot_NilTeamReturnsSnapshot verifies the backwards-compatible path:
// when no team is provided the query is not team-scoped and still returns the snapshot.
func TestGetLastSnapshot_NilTeamReturnsSnapshot(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	result := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	snapshot, err := db.SqlcClient.GetLastSnapshot(ctx, queries.GetLastSnapshotParams{
		SandboxID: sandboxID,
		TeamID:    nil,
	})
	require.NoError(t, err)

	assert.Equal(t, result.BuildID, snapshot.EnvBuild.ID,
		"GetLastSnapshot without team scoping should still return the snapshot build")
}

package snapshotcache

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

// TestSnapshotCache_TeamScopedIsolationAcrossTeams verifies that a snapshot fetched
// through the cache is scoped to the owning team: the owner reads it, while another
// team querying the identical sandbox ID gets a not-found result (no cross-team leak
// and no cache-key collision between teams).
func TestSnapshotCache_TeamScopedIsolationAcrossTeams(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	sc := NewSnapshotCache(db.SqlcClient, redisClient)
	defer sc.Close(ctx)

	ownerTeamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, ownerTeamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()
	result := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, ownerTeamID, baseTemplateID)

	// Owner team reads its own snapshot.
	info, err := sc.Get(ctx, sandboxID, ownerTeamID)
	require.NoError(t, err)
	assert.Equal(t, result.BuildID, info.EnvBuild.ID,
		"owning team should read the snapshot build through the cache")

	// A different team must not see it, even though the sandbox ID is identical.
	otherTeamID := testutils.CreateTestTeam(t, db)
	_, err = sc.Get(ctx, sandboxID, otherTeamID)
	require.ErrorIs(t, err, ErrSnapshotNotFound,
		"a non-owning team must not read another team's snapshot")
}

// TestSnapshotCache_InvalidateClearsTeamScopedKey verifies that Invalidate clears the
// team-scoped cache key (sandboxID:teamID) and not only the plain sandboxID key.
// It first caches a negative (not-found) result under the team-scoped key, then proves
// the negative result is served from cache, and finally that Invalidate forces a
// re-fetch that now finds the snapshot.
func TestSnapshotCache_InvalidateClearsTeamScopedKey(t *testing.T) {
	t.Parallel()
	db := testutils.SetupDatabase(t)
	redisClient := redis_utils.SetupInstance(t)
	ctx := t.Context()

	sc := NewSnapshotCache(db.SqlcClient, redisClient)
	defer sc.Close(ctx)

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)

	sandboxID := "sandbox-" + uuid.New().String()
	snapshotTemplateID := "snapshot-template-" + uuid.New().String()

	// No snapshot yet: not-found, cached under the team-scoped key.
	_, err := sc.Get(ctx, sandboxID, teamID)
	require.ErrorIs(t, err, ErrSnapshotNotFound)

	// The snapshot now appears in the DB.
	result := testutils.UpsertTestSnapshot(t, ctx, db, snapshotTemplateID, sandboxID, teamID, baseTemplateID)

	// The cached negative result is still served, proving the team-scoped key is cached.
	_, err = sc.Get(ctx, sandboxID, teamID)
	require.ErrorIs(t, err, ErrSnapshotNotFound,
		"the team-scoped negative result should be served from cache before invalidation")

	// Invalidate must delete the team-scoped key (sandboxID:teamID) via prefix deletion.
	sc.Invalidate(ctx, sandboxID)

	info, err := sc.Get(ctx, sandboxID, teamID)
	require.NoError(t, err, "after Invalidate the team-scoped key must be re-fetched from the DB")
	assert.Equal(t, result.BuildID, info.EnvBuild.ID)
}

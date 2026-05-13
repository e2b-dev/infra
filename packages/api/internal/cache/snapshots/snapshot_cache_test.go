package snapshotcache
package snapshotcache

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
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func setupCache(t *testing.T) (*SnapshotCache, *testutils.Database) {
	t.Helper()
	db := testutils.SetupDatabase(t)
	redis := redis_utils.SetupInstance(t)
	cache := NewSnapshotCache(db.SqlcClient, redis)
	t.Cleanup(func() { _ = cache.Close(t.Context()) })

	return cache, db
}

func upsertSnapshot(t *testing.T, db *testutils.Database, teamID uuid.UUID, baseTemplateID string) (sandboxID string) {
	t.Helper()
	sandboxID = "sandbox-" + uuid.New().String()
	envdVersion := "v1.0.0"
	totalDisk := int64(1024)
	allowInternet := true

	_, err := db.SqlcClient.UpsertSnapshot(t.Context(), queries.UpsertSnapshotParams{
		TemplateID:          "tmpl-" + uuid.New().String(),
		TeamID:              teamID,
		SandboxID:           sandboxID,
		BaseTemplateID:      baseTemplateID,
		StartedAt:           pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Vcpu:                2,
		RamMb:               2048,
		TotalDiskSizeMb:     &totalDisk,
		Metadata:            types.JSONBStringMap{},
		KernelVersion:       "6.1.0",
		FirecrackerVersion:  "1.4.0",
		EnvdVersion:         &envdVersion,
		Secure:              false,
		AllowInternetAccess: &allowInternet,
		AutoPause:           true,
		OriginNodeID:        "test-node",
		Status:              types.BuildStatusSuccess,
	})
	require.NoError(t, err)

	return sandboxID
}

// TestSnapshotCache_GetByTeam_HitCorrectTeam verifies that GetByTeam returns the
// snapshot when the teamID matches the snapshot owner.
func TestSnapshotCache_GetByTeam_HitCorrectTeam(t *testing.T) {
	t.Parallel()
	cache, db := setupCache(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)
	sandboxID := upsertSnapshot(t, db, teamID, baseTemplateID)

	info, err := cache.GetByTeam(ctx, sandboxID, teamID)
	require.NoError(t, err)
	assert.Equal(t, sandboxID, info.Snapshot.SandboxID)
	assert.Equal(t, teamID, info.Snapshot.TeamID)
}

// TestSnapshotCache_GetByTeam_WrongTeamReturnsNotFound verifies that GetByTeam
// returns ErrSnapshotNotFound when the teamID does not match the snapshot owner.
func TestSnapshotCache_GetByTeam_WrongTeamReturnsNotFound(t *testing.T) {
	t.Parallel()
	cache, db := setupCache(t)
	ctx := t.Context()

	ownerTeamID := testutils.CreateTestTeam(t, db)
	otherTeamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, ownerTeamID)
	sandboxID := upsertSnapshot(t, db, ownerTeamID, baseTemplateID)

	// Warm the cache with the owner's team entry.
	_, err := cache.Get(ctx, sandboxID)
	require.NoError(t, err)

	// Now query with a different team – should fall back to DB and return not-found.
	_, err = cache.GetByTeam(ctx, sandboxID, otherTeamID)
	require.ErrorIs(t, err, ErrSnapshotNotFound)
}

// TestSnapshotCache_GetByTeam_CacheHitFastPath verifies that when the cached entry
// already belongs to the requested team, GetByTeam returns it without a DB round-trip.
// We verify this indirectly: after the first call populates the cache, a second call
// with the same teamID must also succeed.
func TestSnapshotCache_GetByTeam_CacheHitFastPath(t *testing.T) {
	t.Parallel()
	cache, db := setupCache(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)
	sandboxID := upsertSnapshot(t, db, teamID, baseTemplateID)

	// First call – populates cache.
	info1, err := cache.GetByTeam(ctx, sandboxID, teamID)
	require.NoError(t, err)

	// Second call – should hit cache fast path.
	info2, err := cache.GetByTeam(ctx, sandboxID, teamID)
	require.NoError(t, err)

	assert.Equal(t, info1.Snapshot.SandboxID, info2.Snapshot.SandboxID)
	assert.Equal(t, info1.EnvBuild.ID, info2.EnvBuild.ID)
}

// TestSnapshotCache_GetByTeam_UnknownSandboxReturnsNotFound verifies that
// GetByTeam returns ErrSnapshotNotFound for a sandboxID that does not exist.
func TestSnapshotCache_GetByTeam_UnknownSandboxReturnsNotFound(t *testing.T) {
	t.Parallel()
	cache, db := setupCache(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)

	_, err := cache.GetByTeam(ctx, "nonexistent-sandbox-"+uuid.New().String(), teamID)
	require.ErrorIs(t, err, ErrSnapshotNotFound)
}

// TestSnapshotCache_GetByTeam_InvalidateFlushesCache verifies that after Invalidate
// is called, GetByTeam re-fetches from the DB.
func TestSnapshotCache_GetByTeam_InvalidateFlushesCache(t *testing.T) {
	t.Parallel()
	cache, db := setupCache(t)
	ctx := t.Context()

	teamID := testutils.CreateTestTeam(t, db)
	baseTemplateID := testutils.CreateTestTemplate(t, db, teamID)
	sandboxID := upsertSnapshot(t, db, teamID, baseTemplateID)

	// Populate cache.
	_, err := cache.GetByTeam(ctx, sandboxID, teamID)
	require.NoError(t, err)

	// Invalidate.
	cache.Invalidate(ctx, sandboxID)

	// Should still succeed (re-fetches from DB).
	info, err := cache.GetByTeam(ctx, sandboxID, teamID)
	require.NoError(t, err)
	assert.Equal(t, sandboxID, info.Snapshot.SandboxID)
}

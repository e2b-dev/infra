package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

// TeamItems is on the sandbox-listing request path and had no coverage. These
// pin its observable behaviour — which records it returns, which it filters,
// and what it tolerates — so that sharing its decode step with the scanner can
// be shown to preserve it rather than merely asserted to.

// seedTeamSandbox writes one sandbox record and its team index entry directly,
// bypassing Storage.Add so the record's state can be set freely.
func seedTeamSandbox(t *testing.T, teamID uuid.UUID, sandboxID string, state sandboxtypes.State) sandboxtypes.Sandbox {
	t.Helper()

	sbx := makeIndexedSandbox(teamID, sandboxID, uuid.NewString(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	sbx.State = state

	return sbx
}

func writeTeamSandbox(t *testing.T, storage *Storage, sbx sandboxtypes.Sandbox) {
	t.Helper()

	data, err := json.Marshal(sbx)
	require.NoError(t, err)

	team := sbx.TeamID.String()
	require.NoError(t, storage.redisClient.Set(t.Context(), getSandboxKey(team, sbx.SandboxID), data, 0).Err())
	require.NoError(t, storage.redisClient.SAdd(t.Context(), GetSandboxStorageTeamIndexKey(team), sbx.SandboxID).Err())
}

func sandboxIDsOf(sbxs []sandboxtypes.Sandbox) []string {
	ids := make([]string, 0, len(sbxs))
	for _, s := range sbxs {
		ids = append(ids, s.SandboxID)
	}

	return ids
}

// enableCache forces the sandbox cache on for the duration of the test.
// It sets the internal cacheForced field, which bypasses the LaunchDarkly
// feature flag so tests work without a real feature-flag client.
func enableCache(t *testing.T, storage *Storage) {
	t.Helper()

	storage.cacheForced = true
	t.Cleanup(func() { storage.cacheForced = false })
}

// ── Redis-only path (cache disabled) ─────────────────────────────────────────

func TestTeamItems_FiltersByState(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	teamID := uuid.New()

	running := seedTeamSandbox(t, teamID, "sbx-running", sandboxtypes.StateRunning)
	killing := seedTeamSandbox(t, teamID, "sbx-killing", sandboxtypes.StateKilling)
	pausing := seedTeamSandbox(t, teamID, "sbx-pausing", sandboxtypes.StatePausing)
	for _, sbx := range []sandboxtypes.Sandbox{running, killing, pausing} {
		writeTeamSandbox(t, storage, sbx)
	}

	t.Run("single state", func(t *testing.T) {
		t.Parallel()

		items, err := storage.TeamItems(t.Context(), teamID, []sandboxtypes.State{sandboxtypes.StateRunning})
		require.NoError(t, err)
		assert.Equal(t, []string{"sbx-running"}, sandboxIDsOf(items))
	})

	t.Run("several states", func(t *testing.T) {
		t.Parallel()

		items, err := storage.TeamItems(t.Context(), teamID, []sandboxtypes.State{sandboxtypes.StateRunning, sandboxtypes.StatePausing})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"sbx-running", "sbx-pausing"}, sandboxIDsOf(items))
	})

	t.Run("no states returns every record", func(t *testing.T) {
		t.Parallel()

		items, err := storage.TeamItems(t.Context(), teamID, nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"sbx-running", "sbx-killing", "sbx-pausing"}, sandboxIDsOf(items))
	})
}

// TestTeamItems_ToleratesStaleIndexEntries pins the tolerance the scanner's
// decode also relies on: an ID left in the team index after its record was
// deleted is skipped, not an error.
func TestTeamItems_ToleratesStaleIndexEntries(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	teamID := uuid.New()

	writeTeamSandbox(t, storage, seedTeamSandbox(t, teamID, "sbx-live", sandboxtypes.StateRunning))
	require.NoError(t, client.SAdd(t.Context(), GetSandboxStorageTeamIndexKey(teamID.String()), "sbx-deleted").Err())

	items, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sbx-live"}, sandboxIDsOf(items))
}

// TestTeamItems_SkipsUndecodableRecord pins that one corrupt record does not
// fail the listing for the rest of the team.
func TestTeamItems_SkipsUndecodableRecord(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	teamID := uuid.New()

	writeTeamSandbox(t, storage, seedTeamSandbox(t, teamID, "sbx-live", sandboxtypes.StateRunning))

	require.NoError(t, client.Set(t.Context(), getSandboxKey(teamID.String(), "sbx-corrupt"), "not-json", 0).Err())
	require.NoError(t, client.SAdd(t.Context(), GetSandboxStorageTeamIndexKey(teamID.String()), "sbx-corrupt").Err())

	items, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sbx-live"}, sandboxIDsOf(items))
}

func TestTeamItems_EmptyTeam(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)

	items, err := storage.TeamItems(t.Context(), uuid.New(), nil)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestTeamItems_IsScopedToOneTeam pins that the listing cannot leak another
// team's sandboxes — the reason the decode has to stay team-keyed.
func TestTeamItems_IsScopedToOneTeam(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	teamA, teamB := uuid.New(), uuid.New()

	writeTeamSandbox(t, storage, seedTeamSandbox(t, teamA, "sbx-a", sandboxtypes.StateRunning))
	writeTeamSandbox(t, storage, seedTeamSandbox(t, teamB, "sbx-b", sandboxtypes.StateRunning))

	items, err := storage.TeamItems(t.Context(), teamA, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sbx-a"}, sandboxIDsOf(items))
}

func TestTeamItems_BatchesLargeTeams(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	teamID := uuid.New()

	const n = 513 // 2×256+1: exercises all three MGET batches
	for i := range n {
		sbx := seedTeamSandbox(t, teamID, uuid.NewString(), sandboxtypes.StateRunning)
		sbx.SandboxID = sbx.SandboxID + "-" + string(rune('a'+i%26))
		writeTeamSandbox(t, storage, sbx)
	}

	items, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Len(t, items, n)
}

// ── Cache path ────────────────────────────────────────────────────────────────

// TestTeamItems_CacheColdStartWarms verifies that the first TeamItems call
// populates the cache and subsequent calls hit memory.
func TestTeamItems_CacheColdStartWarms(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamID := uuid.New()

	sbx := seedTeamSandbox(t, teamID, "sbx-warm", sandboxtypes.StateRunning)
	writeTeamSandbox(t, storage, sbx)

	// First call: cache is cold → falls back to Redis and warms.
	items, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sbx-warm"}, sandboxIDsOf(items))
	assert.True(t, storage.subManager.cache.isWarm(teamID.String()), "cache should be warm after cold-fetch")

	// Second call: cache is warm → zero Redis reads (no way to count Redis
	// commands in unit tests, but we verify the result is still correct).
	items2, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sbx-warm"}, sandboxIDsOf(items2))
}

// TestTeamItems_CacheEmptyTeamIsWarm verifies that a team with no sandboxes
// is marked warm after the cold-fetch so it does not hit Redis on every call.
func TestTeamItems_CacheEmptyTeamIsWarm(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamID := uuid.New()

	items, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.True(t, storage.subManager.cache.isWarm(teamID.String()))
}

// TestTeamItems_CacheReflectsAddEvent verifies that after the cache is warm,
// an add event (delivered via pub/sub) makes the new sandbox visible without
// a Redis round-trip.
func TestTeamItems_CacheReflectsAddEvent(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamID := uuid.New()

	// Warm with empty team.
	_, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)

	// Simulate an add event arriving from another allocation.
	sbx := seedTeamSandbox(t, teamID, "sbx-via-event", sandboxtypes.StateRunning)
	storage.subManager.cache.apply(sandboxEvent{Op: sandboxEventOpAdd, Sandbox: &sbx})

	items, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sbx-via-event"}, sandboxIDsOf(items))
}

// TestTeamItems_CacheReflectsRemoveEvent verifies that a remove event evicts
// the sandbox from the cache.
func TestTeamItems_CacheReflectsRemoveEvent(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamID := uuid.New()

	sbx := seedTeamSandbox(t, teamID, "sbx-to-remove", sandboxtypes.StateRunning)
	writeTeamSandbox(t, storage, sbx)

	// Warm the cache.
	_, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)

	// Simulate a remove event.
	storage.subManager.cache.apply(sandboxEvent{
		Op:        sandboxEventOpRemove,
		SandboxID: sbx.SandboxID,
		TeamID:    teamID.String(),
	})

	items, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestTeamItems_CacheAddBroadcastedViaStorage verifies the end-to-end path:
// Storage.Add publishes an event that is received by the subscriptionManager
// and applied to the local cache so TeamItems sees the new sandbox.
func TestTeamItems_CacheAddBroadcastedViaStorage(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamID := uuid.New()

	// Warm with empty team so the cache is active.
	_, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)

	// Add via the real Storage.Add path (writes Redis + publishes event).
	sbx := makeIndexedSandbox(teamID, "sbx-e2e", uuid.NewString(), time.Now(), time.Now().Add(time.Hour))
	sbx.State = sandboxtypes.StateRunning
	require.NoError(t, storage.Add(t.Context(), sbx))

	// The event is delivered asynchronously; poll with a short deadline.
	require.Eventually(t, func() bool {
		items, err := storage.TeamItems(t.Context(), teamID, nil)
		if err != nil {
			return false
		}
		return len(items) == 1 && items[0].SandboxID == "sbx-e2e"
	}, 2*time.Second, 50*time.Millisecond, "cache should reflect Add within 2s")
}

// TestTeamItems_CacheRemoveBroadcastedViaStorage verifies that Storage.Remove
// publishes an event that evicts the sandbox from the cache.
func TestTeamItems_CacheRemoveBroadcastedViaStorage(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamID := uuid.New()

	sbx := makeIndexedSandbox(teamID, "sbx-e2e-rm", uuid.NewString(), time.Now(), time.Now().Add(time.Hour))
	sbx.State = sandboxtypes.StateRunning
	require.NoError(t, storage.Add(t.Context(), sbx))

	// Warm the cache.
	_, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)

	// Remove via the real path.
	require.NoError(t, storage.Remove(context.WithoutCancel(t.Context()), teamID, sbx.SandboxID))

	require.Eventually(t, func() bool {
		items, err := storage.TeamItems(t.Context(), teamID, nil)
		return err == nil && len(items) == 0
	}, 2*time.Second, 50*time.Millisecond, "cache should reflect Remove within 2s")
}

// TestTeamItems_CacheUpdateBroadcastedViaStorage verifies that Storage.Update
// publishes an event that refreshes the cached sandbox state.
func TestTeamItems_CacheUpdateBroadcastedViaStorage(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamID := uuid.New()

	sbx := makeIndexedSandbox(teamID, "sbx-e2e-upd", uuid.NewString(), time.Now(), time.Now().Add(time.Hour))
	sbx.State = sandboxtypes.StateRunning
	require.NoError(t, storage.Add(t.Context(), sbx))

	// Warm the cache.
	_, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)

	// Update state via the real path.
	_, err = storage.Update(t.Context(), teamID, sbx.SandboxID, func(s sandboxtypes.Sandbox) (sandboxtypes.Sandbox, error) {
		s.State = sandboxtypes.StatePausing

		return s, nil
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		items, err := storage.TeamItems(t.Context(), teamID, nil)
		if err != nil || len(items) != 1 {
			return false
		}

		return items[0].State == sandboxtypes.StatePausing
	}, 2*time.Second, 50*time.Millisecond, "cache should reflect Update within 2s")
}

// TestTeamItems_CacheIsolatesTeams verifies that events for one team cannot
// affect another team's results when the cache is enabled.
func TestTeamItems_CacheIsolatesTeams(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	enableCache(t, storage)
	teamA, teamB := uuid.New(), uuid.New()

	sbxA := seedTeamSandbox(t, teamA, "sbx-a", sandboxtypes.StateRunning)
	sbxB := seedTeamSandbox(t, teamB, "sbx-b", sandboxtypes.StateRunning)
	writeTeamSandbox(t, storage, sbxA)
	writeTeamSandbox(t, storage, sbxB)

	// Warm both teams.
	_, err := storage.TeamItems(t.Context(), teamA, nil)
	require.NoError(t, err)
	_, err = storage.TeamItems(t.Context(), teamB, nil)
	require.NoError(t, err)

	// Remove sbx-a; sbx-b must remain.
	storage.subManager.cache.apply(sandboxEvent{
		Op: sandboxEventOpRemove, SandboxID: sbxA.SandboxID, TeamID: teamA.String(),
	})

	gotA, err := storage.TeamItems(t.Context(), teamA, nil)
	require.NoError(t, err)
	assert.Empty(t, gotA)

	gotB, err := storage.TeamItems(t.Context(), teamB, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sbx-b"}, sandboxIDsOf(gotB))
}

// TestTeamItems_CacheDisabledByDefault verifies that with no flag override
// the cache is not consulted (team is never warmed via TeamItems alone).
func TestTeamItems_CacheDisabledByDefault(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	// No enableCache call — flag defaults to false.
	teamID := uuid.New()

	sbx := seedTeamSandbox(t, teamID, "sbx-nocache", sandboxtypes.StateRunning)
	writeTeamSandbox(t, storage, sbx)

	_, err := storage.TeamItems(t.Context(), teamID, nil)
	require.NoError(t, err)

	assert.False(t, storage.subManager.cache.isWarm(teamID.String()),
		"cache should NOT be warmed when flag is off")
}

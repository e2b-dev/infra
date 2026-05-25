package redis

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

// TestCleaner_PrunesOrphanedExpirationEntry is the smoke test for the whole
// feature: if a member sits in globalExpirationSet without a matching sandbox
// JSON key, the cleaner must ZREM it. This is the dominant leak source —
// an Add that wrote globalExpirationSet (step 1) but failed before the
// atomic SET+SADD (step 2 via addSandboxScript).
func TestCleaner_PrunesOrphanedExpirationEntry(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := t.Context()

	teamID := uuid.New().String()
	sandboxID := "ghost-" + uuid.NewString()
	member := expirationMember(teamID, sandboxID)

	// Plant an orphan: ZSET entry with old score, no sandbox JSON key.
	require.NoError(t, client.ZAdd(ctx, globalExpirationSet, redis.Z{
		Score:  float64(time.Now().Add(-time.Hour).UnixMilli()),
		Member: member,
	}).Err())

	cleaner := NewCleaner(storage)
	require.NoError(t, cleaner.RunOnce(ctx))

	_, err := client.ZScore(ctx, globalExpirationSet, member).Result()
	require.ErrorIs(t, err, redis.Nil, "orphan should have been ZREM'd from globalExpirationSet")
}

// TestCleaner_PreservesLiveEntries is the negative regression guard: a real
// sandbox added through Storage.Add must survive a RunOnce in all indexes
// plus its JSON key.
func TestCleaner_PreservesLiveEntries(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := t.Context()

	sbx := createTestSandbox("live-" + uuid.NewString())
	require.NoError(t, storage.Add(ctx, sbx))

	cleaner := NewCleaner(storage)
	require.NoError(t, cleaner.RunOnce(ctx))

	// globalExpirationSet still has it.
	_, err := client.ZScore(ctx, globalExpirationSet,
		expirationMember(sbx.TeamID.String(), sbx.SandboxID)).Result()
	require.NoError(t, err, "live entry should remain in globalExpirationSet")

	// globalTeamsSet still has the team.
	_, err = client.ZScore(ctx, globalTeamsSet, sbx.TeamID.String()).Result()
	require.NoError(t, err, "live team should remain in globalTeamsSet")

	// Per-team SET still has the sandbox ID.
	isMember, err := client.SIsMember(ctx,
		GetSandboxStorageTeamIndexKey(sbx.TeamID.String()), sbx.SandboxID).Result()
	require.NoError(t, err)
	require.True(t, isMember, "live sandbox should remain in per-team index")

	// Sandbox JSON itself is untouched.
	got, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err)
	require.Equal(t, sbx.SandboxID, got.SandboxID)
}

// TestCleaner_PrunesEmptyOldTeam plants a stale entry in globalTeamsSet whose
// per-team SET is empty; TeamsWithSandboxCount must ZREM it.
func TestCleaner_PrunesEmptyOldTeam(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := t.Context()

	staleTeamID := uuid.New().String()
	oldScore := float64(time.Now().Add(-time.Hour).Unix())

	require.NoError(t, client.ZAdd(ctx, globalTeamsSet, redis.Z{
		Score:  oldScore,
		Member: staleTeamID,
	}).Err())

	cleaner := NewCleaner(storage)
	require.NoError(t, cleaner.RunOnce(ctx))

	_, err := client.ZScore(ctx, globalTeamsSet, staleTeamID).Result()
	require.ErrorIs(t, err, redis.Nil, "stale empty team should have been ZREM'd")
}

// TestCleaner_PreservesYoungEmptyTeam validates the StaleCutoff race guard:
// an empty team that was added recently must NOT be pruned, because Add
// writes the team entry before the per-team SET SADD (operations.go:33-47)
// so an empty SET on a fresh team can be a transient in-flight Add.
func TestCleaner_PreservesYoungEmptyTeam(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := t.Context()

	youngTeamID := uuid.New().String()

	require.NoError(t, client.ZAdd(ctx, globalTeamsSet, redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: youngTeamID,
	}).Err())

	cleaner := NewCleaner(storage)
	require.NoError(t, cleaner.RunOnce(ctx))

	_, err := client.ZScore(ctx, globalTeamsSet, youngTeamID).Result()
	require.NoError(t, err, "young empty team should be preserved (race guard)")
}

// TestCleaner_ConcurrentRunsConverge proves the multi-pod safety contract:
// running RunOnce from N goroutines against the same Redis must produce
// the same final state as one run, with no errors.
func TestCleaner_ConcurrentRunsConverge(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := t.Context()

	const n = 50
	for i := range n {
		teamID := uuid.New().String()
		sandboxID := fmt.Sprintf("ghost-%d", i)
		require.NoError(t, client.ZAdd(ctx, globalExpirationSet, redis.Z{
			Score:  float64(time.Now().Add(-time.Hour).UnixMilli()),
			Member: expirationMember(teamID, sandboxID),
		}).Err())
	}

	initial, err := client.ZCard(ctx, globalExpirationSet).Result()
	require.NoError(t, err)
	require.EqualValues(t, n, initial)

	cleaner := NewCleaner(storage)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			assert.NoError(t, cleaner.RunOnce(ctx))
		})
	}
	wg.Wait()

	final, err := client.ZCard(ctx, globalExpirationSet).Result()
	require.NoError(t, err)
	require.Zero(t, final, "all orphans should be pruned despite concurrent runs")
}

// TestCleaner_StartExitsOnContextCancel guards against a goroutine leak —
// the Start loop must return promptly when its context is cancelled.
func TestCleaner_StartExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	cleaner := NewCleaner(storage)
	cleaner.tick = 10 * time.Millisecond // tighten so a tick happens during the test

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		cleaner.Start(ctx)
		close(done)
	}()

	// Let at least one tick fire, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Cleaner.Start did not exit on ctx.Done()")
	}
}

// TestCleaner_PreservesFutureScoredExpirationEntry guards the score-based
// filter inside ExpiredItems: a member whose score is in the future
// (sandbox still running) but whose JSON is briefly missing must not be
// touched, because ExpiredItems' ZRangeByScore filters by score <= now.
func TestCleaner_PreservesFutureScoredExpirationEntry(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := t.Context()

	teamID := uuid.New().String()
	sandboxID := "future-" + uuid.NewString()
	member := expirationMember(teamID, sandboxID)

	// Score in the future — outside the ZRangeByScore window in ExpiredItems.
	require.NoError(t, client.ZAdd(ctx, globalExpirationSet, redis.Z{
		Score:  float64(time.Now().Add(time.Hour).UnixMilli()),
		Member: member,
	}).Err())

	cleaner := NewCleaner(storage)
	require.NoError(t, cleaner.RunOnce(ctx))

	_, err := client.ZScore(ctx, globalExpirationSet, member).Result()
	require.NoError(t, err, "future-scored entry must not be pruned")
}

// TestCleaner_EvictsStaleExpiredSandbox covers the new evictExpired path:
// a sandbox whose EndTime is older than StaleCutoff must be Remove()'d by
// the cleaner so its JSON key, per-team index entry, and globalExpirationSet
// member all disappear.
func TestCleaner_EvictsStaleExpiredSandbox(t *testing.T) {
	t.Parallel()

	storage, client := setupTestStorage(t)
	ctx := t.Context()

	sbx := createTestSandbox("stale-expired-" + uuid.NewString())
	sbx.EndTime = time.Now().Add(-sandboxtypes.StaleCutoff - time.Minute)
	require.NoError(t, storage.Add(ctx, sbx))

	cleaner := NewCleaner(storage)
	require.NoError(t, cleaner.RunOnce(ctx))

	_, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.ErrorIs(t, err, sandboxtypes.ErrNotFound, "stale expired sandbox JSON should be removed")

	_, err = client.ZScore(ctx, globalExpirationSet,
		expirationMember(sbx.TeamID.String(), sbx.SandboxID)).Result()
	require.ErrorIs(t, err, redis.Nil, "stale expired sandbox should be removed from globalExpirationSet")

	isMember, err := client.SIsMember(ctx,
		GetSandboxStorageTeamIndexKey(sbx.TeamID.String()), sbx.SandboxID).Result()
	require.NoError(t, err)
	require.False(t, isMember, "stale expired sandbox should be removed from per-team index")
}

// TestCleaner_PreservesRecentlyExpiredSandbox guards the StaleCutoff window
// inside evictExpired: a sandbox that has just expired (EndTime in the past
// but newer than StaleCutoff) is still the evictor's responsibility — the
// cleaner must leave it alone so we don't race the evictor.
func TestCleaner_PreservesRecentlyExpiredSandbox(t *testing.T) {
	t.Parallel()

	storage, _ := setupTestStorage(t)
	ctx := t.Context()

	sbx := createTestSandbox("fresh-expired-" + uuid.NewString())
	sbx.EndTime = time.Now().Add(-time.Second)
	require.NoError(t, storage.Add(ctx, sbx))

	cleaner := NewCleaner(storage)
	require.NoError(t, cleaner.RunOnce(ctx))

	got, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
	require.NoError(t, err, "recently expired sandbox must survive — eviction is the evictor's job")
	require.Equal(t, sbx.SandboxID, got.SandboxID)
}

// Compile-time guard so future refactors of sandboxtypes.StaleCutoff get noticed
// here: the cleaner's correctness depends on it being > 0.
var _ = func() bool {
	if sandboxtypes.StaleCutoff <= 0 {
		panic("sandboxtypes.StaleCutoff must be positive for cleaner race guards to hold")
	}

	return true
}()

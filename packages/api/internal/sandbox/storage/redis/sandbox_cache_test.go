package redis

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

func makeCacheSandbox(teamID uuid.UUID, sandboxID string, state sandboxtypes.State) sandboxtypes.Sandbox {
	return sandboxtypes.Sandbox{
		SandboxID:   sandboxID,
		TeamID:      teamID,
		ExecutionID: uuid.NewString(),
		StartTime:   time.Now().Add(-time.Hour),
		EndTime:     time.Now().Add(time.Hour),
		State:       state,
	}
}

// TestSandboxCache_AddAndGet verifies that an add event populates the cache
// and getTeam returns it after warmTeam is called.
func TestSandboxCache_AddAndGet(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	teamID := uuid.New()
	sbx := makeCacheSandbox(teamID, "sbx-1", sandboxtypes.StateRunning)

	c.warmTeam(teamID.String(), []sandboxtypes.Sandbox{sbx})

	got, ok := c.getTeam(teamID, nil)
	require.True(t, ok, "cache should be warm after warmTeam")
	require.Len(t, got, 1)
	assert.Equal(t, sbx.SandboxID, got[0].SandboxID)
}

// TestSandboxCache_ApplyAdd verifies that an add event populates the cache
// even without an explicit warmTeam call, once the team is already warm.
func TestSandboxCache_ApplyAdd(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	teamID := uuid.New()

	// Warm with empty set.
	c.warmTeam(teamID.String(), nil)

	sbx := makeCacheSandbox(teamID, "sbx-2", sandboxtypes.StateRunning)
	c.apply(sandboxEvent{Op: sandboxEventOpAdd, Sandbox: &sbx})

	got, ok := c.getTeam(teamID, nil)
	require.True(t, ok)
	require.Len(t, got, 1)
	assert.Equal(t, "sbx-2", got[0].SandboxID)
}

// TestSandboxCache_ApplyUpdate verifies that an update event replaces the
// existing sandbox entry in the cache.
func TestSandboxCache_ApplyUpdate(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	teamID := uuid.New()
	sbx := makeCacheSandbox(teamID, "sbx-3", sandboxtypes.StateRunning)
	c.warmTeam(teamID.String(), []sandboxtypes.Sandbox{sbx})

	sbx.State = sandboxtypes.StatePausing
	c.apply(sandboxEvent{Op: sandboxEventOpUpdate, Sandbox: &sbx})

	got, ok := c.getTeam(teamID, nil)
	require.True(t, ok)
	require.Len(t, got, 1)
	assert.Equal(t, sandboxtypes.StatePausing, got[0].State)
}

// TestSandboxCache_ApplyRemove verifies that a remove event evicts the entry.
func TestSandboxCache_ApplyRemove(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	teamID := uuid.New()
	sbx := makeCacheSandbox(teamID, "sbx-4", sandboxtypes.StateRunning)
	c.warmTeam(teamID.String(), []sandboxtypes.Sandbox{sbx})

	c.apply(sandboxEvent{Op: sandboxEventOpRemove, SandboxID: sbx.SandboxID, TeamID: teamID.String()})

	got, ok := c.getTeam(teamID, nil)
	require.True(t, ok, "team should still be warm after remove")
	assert.Empty(t, got, "removed sandbox should not appear")
}

// TestSandboxCache_ColdTeamReturnsFalse verifies that getTeam returns false
// for a team that has never been warmed.
func TestSandboxCache_ColdTeamReturnsFalse(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	_, ok := c.getTeam(uuid.New(), nil)
	assert.False(t, ok, "unwarmed team should return false")
}

// TestSandboxCache_FiltersByState verifies state filtering in getTeam.
func TestSandboxCache_FiltersByState(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	teamID := uuid.New()

	sandboxes := []sandboxtypes.Sandbox{
		makeCacheSandbox(teamID, "sbx-running", sandboxtypes.StateRunning),
		makeCacheSandbox(teamID, "sbx-killing", sandboxtypes.StateKilling),
		makeCacheSandbox(teamID, "sbx-pausing", sandboxtypes.StatePausing),
	}
	c.warmTeam(teamID.String(), sandboxes)

	running, ok := c.getTeam(teamID, []sandboxtypes.State{sandboxtypes.StateRunning})
	require.True(t, ok)
	require.Len(t, running, 1)
	assert.Equal(t, "sbx-running", running[0].SandboxID)

	multi, ok := c.getTeam(teamID, []sandboxtypes.State{sandboxtypes.StateRunning, sandboxtypes.StatePausing})
	require.True(t, ok)
	assert.Len(t, multi, 2)

	all, ok := c.getTeam(teamID, nil)
	require.True(t, ok)
	assert.Len(t, all, 3)
}

// TestSandboxCache_TeamIsolation verifies that events for one team do not
// appear in another team's results.
func TestSandboxCache_TeamIsolation(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	teamA, teamB := uuid.New(), uuid.New()

	sbxA := makeCacheSandbox(teamA, "sbx-a", sandboxtypes.StateRunning)
	sbxB := makeCacheSandbox(teamB, "sbx-b", sandboxtypes.StateRunning)

	c.warmTeam(teamA.String(), []sandboxtypes.Sandbox{sbxA})
	c.warmTeam(teamB.String(), []sandboxtypes.Sandbox{sbxB})

	gotA, ok := c.getTeam(teamA, nil)
	require.True(t, ok)
	require.Len(t, gotA, 1)
	assert.Equal(t, "sbx-a", gotA[0].SandboxID)

	gotB, ok := c.getTeam(teamB, nil)
	require.True(t, ok)
	require.Len(t, gotB, 1)
	assert.Equal(t, "sbx-b", gotB[0].SandboxID)
}

// TestSandboxCache_WarmTeamEvictsStaleSandboxes verifies that re-warming a
// team removes sandboxes that are no longer present in the fresh Redis fetch.
func TestSandboxCache_WarmTeamEvictsStaleSandboxes(t *testing.T) {
	t.Parallel()

	c := newSandboxCache()
	teamID := uuid.New()

	old := makeCacheSandbox(teamID, "sbx-old", sandboxtypes.StateRunning)
	fresh := makeCacheSandbox(teamID, "sbx-fresh", sandboxtypes.StateRunning)

	c.warmTeam(teamID.String(), []sandboxtypes.Sandbox{old})

	// Re-warm with only the fresh sandbox — old should be evicted.
	c.warmTeam(teamID.String(), []sandboxtypes.Sandbox{fresh})

	got, ok := c.getTeam(teamID, nil)
	require.True(t, ok)
	require.Len(t, got, 1)
	assert.Equal(t, "sbx-fresh", got[0].SandboxID)
}

// TestSandboxEvent_Roundtrip verifies JSON marshal/unmarshal of sandboxEvent.
func TestSandboxEvent_Roundtrip(t *testing.T) {
	t.Parallel()

	teamID := uuid.New()
	sbx := makeCacheSandbox(teamID, "sbx-roundtrip", sandboxtypes.StateRunning)

	evt := sandboxEvent{Op: sandboxEventOpAdd, Sandbox: &sbx}
	payload, err := marshalSandboxEvent(evt)
	require.NoError(t, err)
	assert.True(t, isSandboxEvent(payload))

	got, ok := parseSandboxEvent(payload)
	require.True(t, ok)
	assert.Equal(t, sandboxEventOpAdd, got.Op)
	require.NotNil(t, got.Sandbox)
	assert.Equal(t, sbx.SandboxID, got.Sandbox.SandboxID)
}

// TestSandboxEvent_RoutingKeyIsNotEvent ensures existing routing-key strings
// are not misidentified as sandbox events.
func TestSandboxEvent_RoutingKeyIsNotEvent(t *testing.T) {
	t.Parallel()

	routingKeys := []string{
		"sandbox:storage:team-abc:transition:sbx-1:txn-1:notify",
		"lock:sandbox:storage:team-abc:sandboxes:sbx-1:notify",
		"",
		"plain-string",
	}

	for _, key := range routingKeys {
		assert.False(t, isSandboxEvent(key), "routing key %q should not be detected as event", key)
	}
}

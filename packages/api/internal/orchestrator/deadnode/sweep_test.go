package deadnode

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func deadNodeTestSandbox(nodeID string, clusterID uuid.UUID, startedAgo time.Duration, now time.Time) sandbox.Sandbox {
	return sandbox.Sandbox{
		SandboxID: "sbx-" + uuid.NewString(),
		TeamID:    uuid.New(),
		NodeID:    nodeID,
		ClusterID: clusterID,
		StartTime: now.Add(-startedAgo),
		State:     sandbox.StateRunning,
	}
}

func refFor(clusterID uuid.UUID, nodeID string) NodeRef {
	return NodeRef{ClusterID: clusterID.String(), NodeID: nodeID}
}

// seenAgo builds a liveness entry for a node last seen the given duration ago.
func seenAgo(now time.Time, ago time.Duration) nodeLiveness {
	return nodeLiveness{lastSeen: now.Add(-ago)}
}

// missingFor builds a liveness entry for a never-seen node whose shared
// first-missing marker was planted the given duration ago.
func missingFor(now time.Time, ago time.Duration) nodeLiveness {
	return nodeLiveness{firstMissing: now.Add(-ago)}
}

func TestCollectDeadNodeSandboxes_RecentlySeenNodeSkipped(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clusterID := uuid.New()

	sbx := deadNodeTestSandbox("node-1", clusterID, time.Hour, now)
	liveness := map[NodeRef]nodeLiveness{
		refFor(clusterID, "node-1"): seenAgo(now, gracePeriod-time.Second),
	}

	toKill := collectDeadNodeSandboxes(now, []sandbox.Sandbox{sbx}, liveness)

	assert.Empty(t, toKill, "a node seen within the grace period must not be swept")
}

func TestCollectDeadNodeSandboxes_StaleLastSeenKills(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clusterID := uuid.New()

	dead1 := deadNodeTestSandbox("node-1", clusterID, time.Hour, now)
	dead2 := deadNodeTestSandbox("node-1", clusterID, 2*time.Hour, now)
	alive := deadNodeTestSandbox("node-2", clusterID, time.Hour, now)
	liveness := map[NodeRef]nodeLiveness{
		refFor(clusterID, "node-1"): seenAgo(now, gracePeriod+time.Second),
		refFor(clusterID, "node-2"): seenAgo(now, time.Second),
	}

	toKill := collectDeadNodeSandboxes(now, []sandbox.Sandbox{dead1, dead2, alive}, liveness)

	require.Len(t, toKill, 2)
	assert.ElementsMatch(t, []string{dead1.SandboxID, dead2.SandboxID}, []string{toKill[0].SandboxID, toKill[1].SandboxID})
}

func TestCollectDeadNodeSandboxes_NeverSeenNodeAgesViaMarker(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clusterID := uuid.New()

	sbx := deadNodeTestSandbox("node-1", clusterID, time.Hour, now)

	// Marker younger than grace: only tracks.
	young := map[NodeRef]nodeLiveness{refFor(clusterID, "node-1"): missingFor(now, gracePeriod-time.Second)}
	assert.Empty(t, collectDeadNodeSandboxes(now, []sandbox.Sandbox{sbx}, young))

	// Marker past grace: kills.
	ripe := map[NodeRef]nodeLiveness{refFor(clusterID, "node-1"): missingFor(now, gracePeriod+time.Second)}
	toKill := collectDeadNodeSandboxes(now, []sandbox.Sandbox{sbx}, ripe)
	require.Len(t, toKill, 1)
	assert.Equal(t, sbx.SandboxID, toKill[0].SandboxID)
}

func TestCollectDeadNodeSandboxes_NoEvidenceSkipped(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clusterID := uuid.New()

	sbx := deadNodeTestSandbox("node-1", clusterID, time.Hour, now)

	// Node absent from the liveness map entirely.
	assert.Empty(t, collectDeadNodeSandboxes(now, []sandbox.Sandbox{sbx}, map[NodeRef]nodeLiveness{}))

	// Node present but with a zero-value entry.
	empty := map[NodeRef]nodeLiveness{refFor(clusterID, "node-1"): {}}
	assert.Empty(t, collectDeadNodeSandboxes(now, []sandbox.Sandbox{sbx}, empty))
}

func TestCollectDeadNodeSandboxes_SameNodeIDDifferentClusters(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clusterA := uuid.New()
	clusterB := uuid.New()

	deadClusterSbx := deadNodeTestSandbox("node-1", clusterA, time.Hour, now)
	freshClusterSbx := deadNodeTestSandbox("node-1", clusterB, time.Hour, now)
	liveness := map[NodeRef]nodeLiveness{
		refFor(clusterA, "node-1"): seenAgo(now, gracePeriod+time.Second),
		refFor(clusterB, "node-1"): seenAgo(now, time.Second),
	}

	toKill := collectDeadNodeSandboxes(now, []sandbox.Sandbox{deadClusterSbx, freshClusterSbx}, liveness)

	require.Len(t, toKill, 1)
	assert.Equal(t, deadClusterSbx.SandboxID, toKill[0].SandboxID)
}

func TestUniqueNodeRefs(t *testing.T) {
	t.Parallel()

	now := time.Now()
	clusterA := uuid.New()
	clusterB := uuid.New()

	refs := uniqueNodeRefs([]sandbox.Sandbox{
		deadNodeTestSandbox("node-1", clusterA, time.Hour, now),
		deadNodeTestSandbox("node-1", clusterA, time.Hour, now),
		deadNodeTestSandbox("node-1", clusterB, time.Hour, now),
		deadNodeTestSandbox("node-2", clusterA, time.Hour, now),
	})

	assert.ElementsMatch(t, []NodeRef{
		refFor(clusterA, "node-1"),
		refFor(clusterB, "node-1"),
		refFor(clusterA, "node-2"),
	}, refs)
}

// --- sweeper loop tests ---

type fakeRemover struct {
	mu      sync.Mutex
	calls   []string
	opts    map[string]sandbox.RemoveOpts
	returns map[string]error // sandboxID -> error
}

func (f *fakeRemover) remove(_ context.Context, _ uuid.UUID, sandboxID string, opts sandbox.RemoveOpts) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, sandboxID)
	if f.opts == nil {
		f.opts = map[string]sandbox.RemoveOpts{}
	}
	f.opts[sandboxID] = opts

	return f.returns[sandboxID]
}

type countingLister struct {
	mu    sync.Mutex
	calls int
	sbxs  []sandbox.Sandbox
	err   error
}

func (l *countingLister) list(context.Context) ([]sandbox.Sandbox, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++

	return l.sbxs, l.err
}

func seedLastSeen(t *testing.T, client redis.UniversalClient, ref NodeRef, ts time.Time) {
	t.Helper()

	require.NoError(t, client.Set(t.Context(), nodeLastSeenKey(ref), strconv.FormatInt(ts.Unix(), 10), nodeLivenessKeyTTL).Err())
}

func newTestSweeper(t *testing.T, enabled bool, client redis.UniversalClient, lister *countingLister, remover *fakeRemover) *Sweeper {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.DeadNodeSweepEnabledFlag.Key()).VariationForAll(enabled))
	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.Background()) })

	return New(
		ff,
		client,
		lister.list,
		time.Now, // discovery fresh by default
		// Pre-gate open by default so decision tests exercise the full path.
		func(time.Duration) bool { return true },
		remover.remove,
	)
}

func TestSweep_FlagDisabledDoesNothing(t *testing.T) {
	t.Parallel()

	remover := &fakeRemover{}
	lister := &countingLister{sbxs: []sandbox.Sandbox{deadNodeTestSandbox("node-1", uuid.New(), time.Hour, time.Now())}}

	// nil redis client: a disabled sweep must return before touching anything.
	s := newTestSweeper(t, false, nil, lister, remover)
	s.sweep(context.Background())

	assert.Empty(t, remover.calls, "disabled sweep must never call removeSandbox")
	assert.Zero(t, lister.calls, "disabled sweep must not scan the store")
}

func TestSweep_StaleDiscoverySkipsCycle(t *testing.T) {
	t.Parallel()

	remover := &fakeRemover{}
	lister := &countingLister{sbxs: []sandbox.Sandbox{deadNodeTestSandbox("node-1", uuid.New(), time.Hour, time.Now())}}

	s := newTestSweeper(t, true, nil, lister, remover)

	for name, last := range map[string]func() time.Time{
		"never synced": func() time.Time { return time.Time{} },
		"stale sync":   func() time.Time { return time.Now().Add(-discoveryFreshnessMax - time.Second) },
	} {
		s.lastDiscoverySync = last
		s.sweep(context.Background())

		assert.Empty(t, remover.calls, "%s: sweep must not act on stale observations", name)
		assert.Zero(t, lister.calls, "%s: sweep must not scan on stale observations", name)
	}
}

func TestSweep_PreGateSkipsScanWhenIdle(t *testing.T) {
	t.Parallel()

	remover := &fakeRemover{}
	lister := &countingLister{}
	s := newTestSweeper(t, true, nil, lister, remover)
	s.anyNodeUnreachablePast = func(time.Duration) bool { return false }

	// Ticks 1..backstopTicks-1: no candidates -> no store scan.
	for range backstopTicks - 1 {
		s.sweep(context.Background())
	}
	assert.Zero(t, lister.calls, "idle ticks must not scan the store")

	// Backstop tick scans unconditionally.
	s.sweep(context.Background())
	assert.Equal(t, 1, lister.calls, "backstop tick must scan")
}

func TestSweep_UnreachableCandidateTriggersScan(t *testing.T) {
	t.Parallel()

	remover := &fakeRemover{}
	lister := &countingLister{}
	s := newTestSweeper(t, true, nil, lister, remover)

	s.sweep(context.Background()) // tick 1, not a backstop tick; pre-gate open

	assert.Equal(t, 1, lister.calls, "a local unreachable candidate must trigger a scan without waiting for the backstop")
}

func TestSweep_ListErrorSkipsCycle(t *testing.T) {
	t.Parallel()

	remover := &fakeRemover{}
	lister := &countingLister{err: errors.New("redis down")}
	s := newTestSweeper(t, true, nil, lister, remover)

	s.sweep(context.Background())

	assert.Empty(t, remover.calls)
}

func TestSweep_LivenessErrorSkipsCycle(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	clusterID := uuid.New()
	sbx := deadNodeTestSandbox("node-1", clusterID, time.Hour, time.Now())
	remover := &fakeRemover{}
	lister := &countingLister{sbxs: []sandbox.Sandbox{sbx}}

	s := newTestSweeper(t, true, client, lister, remover)
	// Break the liveness fetch: closed client errors on every command.
	require.NoError(t, client.Close())

	s.sweep(context.Background())

	assert.Empty(t, remover.calls, "no liveness evidence must never purge")
}

func TestSweep_KillsWithNodeGoneReason(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	clusterID := uuid.New()
	now := time.Now()
	dead := deadNodeTestSandbox("node-dead", clusterID, time.Hour, now)
	alive := deadNodeTestSandbox("node-alive", clusterID, time.Hour, now)

	seedLastSeen(t, client, refFor(clusterID, "node-dead"), now.Add(-gracePeriod-time.Second))
	seedLastSeen(t, client, refFor(clusterID, "node-alive"), now)

	remover := &fakeRemover{}
	lister := &countingLister{sbxs: []sandbox.Sandbox{dead, alive}}

	s := newTestSweeper(t, true, client, lister, remover)
	s.sweep(context.Background())

	require.Equal(t, []string{dead.SandboxID}, remover.calls)
	opts := remover.opts[dead.SandboxID]
	assert.Equal(t, sandbox.StateActionKill, opts.Action)
	assert.Equal(t, sandbox.KillReasonNodeGone, opts.Reason)
	assert.False(t, opts.Eviction)
}

func TestSweep_NeverSeenNodePurgedOnlyAfterMarkerGrace(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	clusterID := uuid.New()
	now := time.Now()
	sbx := deadNodeTestSandbox("node-1", clusterID, time.Hour, now)

	remover := &fakeRemover{}
	lister := &countingLister{sbxs: []sandbox.Sandbox{sbx}}

	// First sweep observes the never-seen node: plants the marker, kills nothing.
	s := newTestSweeper(t, true, client, lister, remover)
	s.sweep(context.Background())
	assert.Empty(t, remover.calls, "first observation must only plant the marker")

	// Backdate the shared marker past the grace period: next sweep purges.
	require.NoError(t, client.Set(t.Context(), nodeFirstMissingKey(refFor(clusterID, "node-1")),
		strconv.FormatInt(now.Add(-gracePeriod-time.Second).Unix(), 10), nodeLivenessKeyTTL).Err())

	s.sweep(context.Background())
	assert.Equal(t, []string{sbx.SandboxID}, remover.calls)
}

func TestSweep_OneRemoveErrorDoesNotStopOthers(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	clusterID := uuid.New()
	now := time.Now()
	a := deadNodeTestSandbox("node-1", clusterID, time.Hour, now)
	b := deadNodeTestSandbox("node-1", clusterID, time.Hour, now)

	seedLastSeen(t, client, refFor(clusterID, "node-1"), now.Add(-gracePeriod-time.Second))

	remover := &fakeRemover{returns: map[string]error{a.SandboxID: errors.New("boom")}}
	lister := &countingLister{sbxs: []sandbox.Sandbox{a, b}}

	s := newTestSweeper(t, true, client, lister, remover)
	s.sweep(context.Background())

	assert.ElementsMatch(t, []string{a.SandboxID, b.SandboxID}, remover.calls)
}

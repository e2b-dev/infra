package deadnode

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func TestNodeLiveness_RecordAndFetch(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	ref := NodeRef{ClusterID: "cluster-a", NodeID: "node-1"}
	now := time.Now()

	require.NoError(t, RecordNodeSeen(t.Context(), client, ref, now))

	liveness, err := fetchNodeLiveness(t.Context(), client, []NodeRef{ref}, now)
	require.NoError(t, err)

	nl, ok := liveness[ref]
	require.True(t, ok)
	assert.WithinDuration(t, now, nl.lastSeen, time.Second)
	assert.True(t, nl.firstMissing.IsZero())
}

func TestNodeLiveness_MissingMarkerIsFirstWriteWins(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	ref := NodeRef{ClusterID: "cluster-a", NodeID: "node-1"}
	first := time.Now().Add(-time.Minute)

	// First observer (e.g. replica A) plants the marker.
	livenessA, err := fetchNodeLiveness(t.Context(), client, []NodeRef{ref}, first)
	require.NoError(t, err)
	require.WithinDuration(t, first, livenessA[ref].firstMissing, time.Second)

	// A later observer (replica B) must read A's marker, not plant its own —
	// the grace period ages from the FIRST observation across all replicas.
	livenessB, err := fetchNodeLiveness(t.Context(), client, []NodeRef{ref}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, livenessA[ref].firstMissing.Unix(), livenessB[ref].firstMissing.Unix())
}

func TestNodeLiveness_RecordSeenClearsMissingMarker(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	ref := NodeRef{ClusterID: "cluster-a", NodeID: "node-1"}
	past := time.Now().Add(-time.Hour)

	// Marker planted long ago.
	_, err := fetchNodeLiveness(t.Context(), client, []NodeRef{ref}, past)
	require.NoError(t, err)

	// Node seen: last-seen written, marker cleared.
	require.NoError(t, RecordNodeSeen(t.Context(), client, ref, time.Now()))

	// Simulate the last-seen key later expiring (node gone again long after):
	// the next missing observation must start a FRESH grace period, not
	// resurrect the old marker.
	require.NoError(t, client.Del(t.Context(), nodeLastSeenKey(ref)).Err())

	now := time.Now()
	liveness, err := fetchNodeLiveness(t.Context(), client, []NodeRef{ref}, now)
	require.NoError(t, err)
	assert.WithinDuration(t, now, liveness[ref].firstMissing, time.Second, "old marker must not survive a successful sync")
}

func TestNodeLiveness_MixedBatch(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	seen := NodeRef{ClusterID: "cluster-a", NodeID: "node-seen"}
	missing := NodeRef{ClusterID: "cluster-a", NodeID: "node-missing"}
	now := time.Now()

	require.NoError(t, RecordNodeSeen(t.Context(), client, seen, now))

	liveness, err := fetchNodeLiveness(t.Context(), client, []NodeRef{seen, missing}, now)
	require.NoError(t, err)
	require.Len(t, liveness, 2)
	assert.False(t, liveness[seen].lastSeen.IsZero())
	assert.False(t, liveness[missing].firstMissing.IsZero())
}

func TestNodeLiveness_EmptyRefs(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)

	liveness, err := fetchNodeLiveness(t.Context(), client, nil, time.Now())
	require.NoError(t, err)
	assert.Empty(t, liveness)
}

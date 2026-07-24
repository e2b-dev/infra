package nodemanager

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func TestUnreachableSince_DefaultReachable(t *testing.T) {
	t.Parallel()

	n := &Node{}

	_, unreachable := n.UnreachableSince()
	assert.False(t, unreachable)
}

func TestMarkUnhealthyLocal_SetsUnreachableSinceOnce(t *testing.T) {
	t.Parallel()

	n := &Node{}
	ctx := context.Background()

	n.markUnhealthyLocal(ctx)
	first, unreachable := n.UnreachableSince()
	require.True(t, unreachable)

	// A repeated local unhealthy observation must preserve the first
	// timestamp so the unreachable duration keeps accumulating.
	time.Sleep(5 * time.Millisecond)
	n.markUnhealthyLocal(ctx)
	second, unreachable := n.UnreachableSince()
	require.True(t, unreachable)
	assert.Equal(t, first, second)

	assert.Equal(t, api.NodeStatusUnhealthy, n.Status())
}

func TestMarkReachable_ClearsUnreachableSince(t *testing.T) {
	t.Parallel()

	n := &Node{}
	ctx := context.Background()

	n.markUnhealthyLocal(ctx)
	n.markReachable()

	_, unreachable := n.UnreachableSince()
	assert.False(t, unreachable)

	// The next unreachable observation starts a fresh clock.
	n.markUnhealthyLocal(ctx)
	since, unreachable := n.UnreachableSince()
	require.True(t, unreachable)
	assert.WithinDuration(t, time.Now(), since, time.Second)
}

// TestSelfReportedUnhealthyIsNotUnreachable pins the distinction the dead-node
// sweep relies on: a node whose orchestrator self-reports Unhealthy via a
// successful sync is responsive and must never be considered unreachable.
func TestSelfReportedUnhealthyIsNotUnreachable(t *testing.T) {
	t.Parallel()

	n := &Node{}
	n.setStatus(context.Background(), api.NodeStatusUnhealthy, time.Now().Add(-time.Hour))

	_, unreachable := n.UnreachableSince()
	assert.False(t, unreachable)
}

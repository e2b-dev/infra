package nodemanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

// TestSync_ShutDownConnectionIsFatal: a node whose conn was closed under it
// (cluster Instance dropped and re-added with the same instance ID) fails every
// RPC forever. Sync must escalate so the caller deregisters — not retry or mark
// unhealthy.
func TestSync_ShutDownConnectionIsFatal(t *testing.T) {
	t.Parallel()

	n := NewTestNode("test-node", api.NodeStatusReady, 0, 1)
	require.NoError(t, n.client.Connection.Close())

	err := n.Sync(t.Context(), nil)
	require.Error(t, err, "a shut-down connection can never recover and must be escalated")

	// StatusInfo() derives an Unhealthy view from the conn state on its own;
	// assert the stored status, which Sync must not touch during teardown.
	assert.Equal(t, api.NodeStatusReady, n.status.Status, "stored status must not churn during teardown")
	_, unreachable := n.UnreachableSince()
	assert.False(t, unreachable, "a closed local conn is not evidence the node is unreachable")
}

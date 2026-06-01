//go:build linux

package chroot

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/chrooted"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

// makeSandbox creates a minimal *sandbox.Sandbox with a unique lifecycleID.
func makeSandbox(lifecycleID string) *sandbox.Sandbox {
	return &sandbox.Sandbox{
		LifecycleID: lifecycleID,
		Metadata: &sandbox.Metadata{
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: uuid.NewString(),
			},
		},
		Resources: &sandbox.Resources{
			Slot: &network.Slot{HostIP: net.IPv4(127, 0, 0, 1)},
		},
	}
}

// countingCounter embeds noop.Int64Counter to satisfy the full metric.Int64Counter
// interface (including the embedded private marker), and overrides Add to record calls.
type countingCounter struct {
	noop.Int64Counter
	total atomic.Int64
}

func (c *countingCounter) Add(_ context.Context, incr int64, _ ...metric.AddOption) {
	c.total.Add(incr)
}

// TestOnNetworkRelease_CounterOnlyIncrementedOnSuccess verifies that
// chrootUnmountsCounter is NOT incremented when Close() returns an error,
// so that mounts-unmounts accurately reflects leaked mount namespaces.
func TestOnNetworkRelease_CounterOnlyIncrementedOnSuccess(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("requires pivot_root, skipping in short mode")
	}

	dir := t.TempDir()
	ctx := context.Background()

	// goodChroot: will close successfully.
	goodChroot, err := chrooted.Chroot(ctx, dir)
	require.NoError(t, err)

	// badChroot: pre-close it so the second Close() returns an error,
	// simulating a mount namespace that is already gone.
	badChroot, err := chrooted.Chroot(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, badChroot.Close())

	lifecycleID := "lifecycle-abc"

	unmounts := &countingCounter{}
	mounts := &countingCounter{}

	h := &NFSHandler{
		chrootsByLifecycleID:  map[string][]*chrooted.Chrooted{lifecycleID: {goodChroot, badChroot}},
		chrootUnmountsCounter: unmounts,
		chrootMountsCounter:   mounts,
	}

	h.OnNetworkRelease(ctx, makeSandbox(lifecycleID))

	// goodChroot closed OK → counter == 1.
	// badChroot.Close() failed → counter must NOT be incremented.
	assert.Equal(t, int64(1), unmounts.total.Load(),
		"unmounts counter must only increment on successful Close()")

	// The lifecycle entry must be removed regardless of Close() errors.
	h.mu.Lock()
	_, exists := h.chrootsByLifecycleID[lifecycleID]
	h.mu.Unlock()
	assert.False(t, exists, "lifecycle entry must be removed from map after OnNetworkRelease")
}

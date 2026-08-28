//go:build linux

package nbd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestCloseMarksTheDescriptorCloseBetweenDrainAndDisconnect pins the boundary
// the change exists for.
//
// Drain and the descriptor close can each block without bound, and a stalled
// close is read by differencing the surrounding event timestamps. If the
// descriptor event is missing, or drifts to either side of the operation it
// marks, the two collapse into one interval and a two-minute stall becomes
// unattributable -- which is the state this replaces, and which a green suite
// would otherwise not notice.
func TestCloseMarksTheDescriptorCloseBetweenDrainAndDisconnect(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("the nbd requires root privileges to run")
	}

	// Close builds its span from the package tracer, so the recorder is the
	// package-wide one installed in TestMain. Selecting on this test's own
	// trace id is what keeps that safe to share with parallel tests.
	ctx, parent := otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd").Start(t.Context(), "test-parent")
	traceID := parent.SpanContext().TraceID()

	const size = int64(16 * 1024 * 1024)

	device, err := testutils.NewZeroDevice(size, header.RootfsBlockSize)
	require.NoError(t, err)

	cowPath := filepath.Join(t.TempDir(), fmt.Sprintf("events-%s.cow", uuid.New().String()))
	cache, err := block.NewCache(size, header.RootfsBlockSize, cowPath, false)
	require.NoError(t, err)

	overlay := block.NewOverlay(device, cache)
	t.Cleanup(func() { _ = overlay.Close() })

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = featureFlags.Close(context.WithoutCancel(t.Context())) })

	pool, err := NewDevicePool(16)
	require.NoError(t, err)

	poolCtx, poolCancel := context.WithCancel(t.Context())
	poolDone := make(chan struct{})
	go func() {
		pool.Populate(poolCtx)
		close(poolDone)
	}()
	t.Cleanup(func() {
		poolCancel()
		<-poolDone
		_ = pool.Close(context.WithoutCancel(t.Context()))
	})

	mnt := NewDirectPathMount(overlay, pool, featureFlags)

	_, err = mnt.Open(ctx)
	require.NoError(t, err)
	require.NoError(t, mnt.Close(ctx))
	parent.End()

	var events []string
	for _, s := range testSpanRecorder.Ended() {
		if s.Name() != "direct-path-mount-close" || s.SpanContext().TraceID() != traceID {
			continue
		}

		for _, e := range s.Events() {
			events = append(events, e.Name)
		}
	}
	require.NotEmpty(t, events, "the close span must record its steps")

	drain := indexOfEvent(t, events, "waiting for pending responses")
	descriptor := indexOfEvent(t, events, "closing NBD device descriptor")
	disconnect := indexOfEvent(t, events, "disconnecting NBD")

	require.Greaterf(t, descriptor, drain,
		"the descriptor mark must follow the drain, else the two are one interval: %v", events)
	require.Lessf(t, descriptor, disconnect,
		"the descriptor mark must precede the disconnect, else the two are one interval: %v", events)
}

func indexOfEvent(t *testing.T, events []string, name string) int {
	t.Helper()

	for i, e := range events {
		if e == name {
			return i
		}
	}

	require.Failf(t, "missing close event", "%q not in %v", name, events)

	return -1
}

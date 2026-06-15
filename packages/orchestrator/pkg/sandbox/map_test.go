//go:build linux

package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

func TestMapMarkRunningTracksLifecycle(t *testing.T) {
	t.Parallel()

	sandboxes := NewSandboxesMap()
	sbx := testMapSandbox(t, "lifecycle-1")

	sandboxes.MarkRunning(t.Context(), sbx)
	require.Len(t, sandboxes.Items(), 1)
	require.Len(t, sandboxes.LifecycleItems(), 1)
}

func TestMapLifecycleItemsRemainAfterMarkStopping(t *testing.T) {
	t.Parallel()

	sandboxes := NewSandboxesMap()
	sbx := testMapSandbox(t, "lifecycle-1")

	sandboxes.MarkRunning(t.Context(), sbx)
	require.Len(t, sandboxes.Items(), 1)
	require.Len(t, sandboxes.LifecycleItems(), 1)

	marked := sandboxes.MarkStopping(t.Context(), sbx.Runtime.SandboxID, sbx.LifecycleID)
	require.True(t, marked)
	require.Empty(t, sandboxes.Items())
	require.Len(t, sandboxes.LifecycleItems(), 1)

	sandboxes.MarkStopped(t.Context(), sbx)
	require.Empty(t, sandboxes.LifecycleItems())
}

func TestSandboxCloseMarksLifecycleStopped(t *testing.T) {
	t.Parallel()

	sandboxes := NewSandboxesMap()
	sbx := testMapSandbox(t, "lifecycle-1")
	sbx.cleanup = NewCleanup()
	sbx.sandboxes = sandboxes

	sandboxes.MarkRunning(t.Context(), sbx)
	require.Len(t, sandboxes.LifecycleItems(), 1)

	require.NoError(t, sbx.Close(t.Context()))
	require.Empty(t, sandboxes.LifecycleItems())
}

func TestMapLifecycleItemsAllowDuplicateSandboxIDs(t *testing.T) {
	t.Parallel()

	sandboxes := NewSandboxesMap()
	oldSbx := testMapSandbox(t, "lifecycle-old")
	newSbx := testMapSandbox(t, "lifecycle-new")

	sandboxes.MarkRunning(t.Context(), oldSbx)
	require.True(t, sandboxes.MarkStopping(t.Context(), oldSbx.Runtime.SandboxID, oldSbx.LifecycleID))
	sandboxes.MarkRunning(t.Context(), newSbx)

	require.Len(t, sandboxes.LifecycleItems(), 2)
}

func TestMapWaitLifecyclesReturnsWhenEmpty(t *testing.T) {
	t.Parallel()

	sandboxes := NewSandboxesMap()

	require.NoError(t, sandboxes.WaitLifecycles(t.Context()))
}

func TestMapWaitLifecyclesWaitsUntilStopped(t *testing.T) {
	t.Parallel()

	sandboxes := NewSandboxesMap()
	sbx := testMapSandbox(t, "lifecycle-1")
	sandboxes.MarkRunning(t.Context(), sbx)

	waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- sandboxes.WaitLifecycles(waitCtx)
	}()

	select {
	case err := <-done:
		require.Failf(t, "WaitLifecycles returned before lifecycle stopped", "err: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	sandboxes.MarkStopped(t.Context(), sbx)
	require.NoError(t, <-done)
}

func TestMapWaitLifecyclesReturnsContextError(t *testing.T) {
	t.Parallel()

	sandboxes := NewSandboxesMap()
	sbx := testMapSandbox(t, "lifecycle-1")
	sandboxes.MarkRunning(t.Context(), sbx)

	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, sandboxes.WaitLifecycles(waitCtx), context.Canceled)
}

func TestMapConcurrentMarkStoppingAndStoppedDoesNotResurrectLifecycle(t *testing.T) {
	t.Parallel()

	for range 1000 {
		sandboxes := NewSandboxesMap()
		sbx := testMapSandbox(t, "lifecycle-1")
		sandboxes.MarkRunning(t.Context(), sbx)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			sandboxes.MarkStopping(t.Context(), sbx.Runtime.SandboxID, sbx.LifecycleID)
		}()

		go func() {
			defer wg.Done()
			<-start
			sandboxes.MarkStopped(t.Context(), sbx)
		}()

		close(start)
		wg.Wait()

		require.Empty(t, sandboxes.LifecycleItems())
	}
}

func testMapSandbox(t *testing.T, lifecycleID string) *Sandbox {
	t.Helper()

	slot, err := network.NewSlot("test", 1, network.Config{}, network.NoopEgressProxy{})
	require.NoError(t, err)

	return &Sandbox{
		LifecycleID: lifecycleID,
		Metadata: &Metadata{
			Config:  NewConfig(Config{}),
			Runtime: RuntimeMetadata{SandboxID: "sandbox-1"},
		},
		Resources: &Resources{Slot: slot},
	}
}

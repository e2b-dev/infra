package cgroups

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkloadFreezer_FreezeHoldBlocksUnfreeze verifies FreezeHold keeps the
// shared lock held so a concurrent Unfreeze cannot thaw the workload until
// release is called — the serialization the live-upgrade handover relies on.
func TestWorkloadFreezer_FreezeHoldBlocksUnfreeze(t *testing.T) {
	t.Parallel()

	f := NewWorkloadFreezer(NewNoopManager())

	release, err := f.FreezeHold(context.Background())
	require.NoError(t, err)

	unfrozen := make(chan struct{})
	go func() {
		_ = f.Unfreeze(context.Background())
		close(unfrozen)
	}()

	select {
	case <-unfrozen:
		t.Fatal("Unfreeze thawed the workload while the freeze hold was active")
	case <-time.After(100 * time.Millisecond):
		// expected: blocked on the held lock
	}

	release()

	select {
	case <-unfrozen:
		// expected: proceeds once the hold is released
	case <-time.After(2 * time.Second):
		t.Fatal("Unfreeze did not proceed after the hold was released")
	}

	assert.NotPanics(t, release, "release must be idempotent")
}

//go:build linux

package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFactoryStartDrainingRejectsNewStarts(t *testing.T) {
	t.Parallel()

	factory := testFactory()
	factory.StartDraining(t.Context())

	_, err := factory.enterSandboxStart(t.Context())
	require.ErrorIs(t, err, ErrFactoryDraining)
}

func TestFactoryHeldStartGateBypassesDrainRejection(t *testing.T) {
	t.Parallel()

	factory := testFactory()
	factory.StartDraining(t.Context())

	// A start on a context that already holds the gate is admitted even while
	// draining (the nested checkpoint resume), without re-entering the counter.
	release, err := factory.enterSandboxStart(WithHeldStartGate(t.Context()))
	require.NoError(t, err)
	release()

	// The held marker does not increment the gate, so nothing is in flight.
	require.NoError(t, factory.WaitSandboxStarts(t.Context()))
}

func TestFactoryWaitSandboxStartsWaitsUntilStartLeaves(t *testing.T) {
	t.Parallel()

	factory := testFactory()
	release, err := factory.enterSandboxStart(t.Context())
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- factory.WaitSandboxStarts(waitCtx)
	}()

	select {
	case err := <-done:
		require.Failf(t, "WaitSandboxStarts returned before start left", "err: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	release()
	require.NoError(t, <-done)
}

func TestFactoryWaitSandboxStartsReturnsContextError(t *testing.T) {
	t.Parallel()

	factory := testFactory()
	release, err := factory.enterSandboxStart(t.Context())
	require.NoError(t, err)
	defer release()

	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, factory.WaitSandboxStarts(waitCtx), context.Canceled)
}

func testFactory() *Factory {
	return &Factory{
		Sandboxes: NewSandboxesMap(),
	}
}

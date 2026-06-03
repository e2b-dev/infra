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

	require.ErrorIs(t, factory.enterSandboxStart(), ErrFactoryDraining)
}

func TestFactoryWaitSandboxStartsWaitsUntilStartLeaves(t *testing.T) {
	t.Parallel()

	factory := testFactory()
	require.NoError(t, factory.enterSandboxStart())

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

	factory.leaveSandboxStart()
	require.NoError(t, <-done)
}

func TestFactoryWaitSandboxStartsReturnsContextError(t *testing.T) {
	t.Parallel()

	factory := testFactory()
	require.NoError(t, factory.enterSandboxStart())
	defer factory.leaveSandboxStart()

	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, factory.WaitSandboxStarts(waitCtx), context.Canceled)
}

func TestFactoryTryWaitSandboxStartsDoesNotBlock(t *testing.T) {
	t.Parallel()

	factory := testFactory()
	require.NoError(t, factory.enterSandboxStart())
	defer factory.leaveSandboxStart()

	require.False(t, factory.TryWaitSandboxStarts(t.Context()))
}

func testFactory() *Factory {
	return &Factory{
		Sandboxes: NewSandboxesMap(),
		drainDone: make(chan struct{}),
	}
}

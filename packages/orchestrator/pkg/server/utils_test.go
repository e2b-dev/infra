//go:build linux

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
)

func TestWaitSandboxStartsCanceledDoesNotBlockDrainingRejection(t *testing.T) {
	t.Parallel()

	s := &Server{done: make(chan struct{})}

	s.sandboxStartMu.RLock()
	defer s.sandboxStartMu.RUnlock()

	waitCtx, cancel := context.WithCancel(t.Context())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.waitSandboxStarts(waitCtx)
	}()

	// Give waitSandboxStarts a chance to observe the held read lock. The old
	// implementation left a queued writer here, which blocked future RLock calls.
	time.Sleep(2 * sandboxStartWaitPollInterval)
	cancel()

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waitSandboxStarts did not return after cancellation")
	}

	close(s.done)

	enterErr := make(chan error, 1)
	go func() {
		enterErr <- s.enterSandboxStart(t.Context(), "test")
	}()

	select {
	case err := <-enterErr:
		if err == nil {
			s.leaveSandboxStart()
		}
		require.Equal(t, codes.Unavailable, status.Code(err))
	case <-time.After(time.Second):
		t.Fatal("enterSandboxStart blocked instead of rejecting while draining")
	}
}
func TestForceStopSandboxesWaitsForInFlightStarts(t *testing.T) {
	t.Parallel()

	s := forceStopTestServer()
	s.sandboxStartMu.RLock()
	locked := true
	defer func() {
		if locked {
			s.sandboxStartMu.RUnlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- s.ForceStopSandboxes(t.Context())
	}()

	select {
	case err := <-done:
		require.Failf(t, "ForceStopSandboxes returned before start left", "err: %v", err)
	case <-time.After(2 * sandboxStartWaitPollInterval):
	}

	s.sandboxStartMu.RUnlock()
	locked = false

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("ForceStopSandboxes did not return after start left")
	}
}

func TestForceStopSandboxesReturnsInFlightStartContextError(t *testing.T) {
	t.Parallel()

	s := forceStopTestServer()
	s.sandboxStartMu.RLock()
	defer s.sandboxStartMu.RUnlock()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, s.ForceStopSandboxes(ctx), context.Canceled)
}

func TestWaitForceStopSandboxesReturnsContextError(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, waitForceStopSandboxes(ctx, &wg), context.Canceled)
}

func forceStopTestServer() *Server {
	return &Server{
		done: make(chan struct{}),
		sandboxFactory: &sandbox.Factory{
			Sandboxes: sandbox.NewSandboxesMap(),
		},
	}
}

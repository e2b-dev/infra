//go:build linux

package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestWaitSandboxStartsCanceledDoesNotBlockDrainingRejection(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	release, err := s.enterSandboxStart(t.Context(), "in-flight")
	require.NoError(t, err)
	defer release()

	waitCtx, cancel := context.WithCancel(t.Context())
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- s.waitSandboxStarts(waitCtx)
	}()

	cancel()

	select {
	case err := <-waitErr:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waitSandboxStarts did not return after cancellation")
	}

	s.StartDraining(t.Context())

	enterErr := make(chan error, 1)
	go func() {
		release, err := s.enterSandboxStart(t.Context(), "test")
		if err == nil {
			release()
		}

		enterErr <- err
	}()

	select {
	case err := <-enterErr:
		require.Equal(t, codes.Unavailable, status.Code(err))
	case <-time.After(time.Second):
		t.Fatal("enterSandboxStart blocked instead of rejecting while draining")
	}
}

// Create enters the start gate, so it is rejected with Unavailable once the node
// is draining. Checkpoint never enters the gate, so it is unaffected by drain.
func TestEnterSandboxStartRejectedWhileDraining(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	s.StartDraining(t.Context())

	_, err := s.enterSandboxStart(t.Context(), "sandbox-create")
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestWaitForAcquireAllowsAdmittedStartAfterDraining(t *testing.T) {
	t.Parallel()

	startingSandboxes, err := sharedutils.NewAdjustableSemaphore(1)
	require.NoError(t, err)

	s := drainOrderTestServer()
	s.startingSandboxes = startingSandboxes

	release, err := s.enterSandboxStart(t.Context(), "test")
	require.NoError(t, err)
	defer release()

	s.StartDraining(t.Context())

	require.NoError(t, s.waitForAcquire(t.Context()))
	s.startingSandboxes.Release(1)
}

// Graceful drain must reject new starts, wait for an admitted in-flight start to
// finish, and only then complete.
func TestDrainSandboxesWaitsForInFlightStart(t *testing.T) {
	t.Parallel()

	s := drainOrderTestServer()
	release, err := s.enterSandboxStart(t.Context(), "in-flight")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- s.DrainSandboxes(t.Context())
	}()

	// Drain begins immediately, before the in-flight start finishes.
	select {
	case <-s.startGate.Done():
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not start draining")
	}
	require.True(t, s.startGate.Draining())

	// But it must not complete while a start is still in flight.
	select {
	case err := <-done:
		require.Failf(t, "DrainSandboxes returned before in-flight start finished", "err: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	release()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("DrainSandboxes did not return after in-flight start finished")
	}
}

func drainOrderTestServer() *Server {
	return &Server{
		sandboxFactory: sandbox.NewFactory(cfg.BuilderConfig{}, nil, nil, nil, nil, nil, nil, sandbox.NewSandboxesMap()),
	}
}

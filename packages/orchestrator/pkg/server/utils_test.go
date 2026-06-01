//go:build linux

package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

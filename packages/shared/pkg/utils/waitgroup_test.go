package utils

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitGroupWaitAlreadyDone(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup

	require.NoError(t, WaitGroupWait(t.Context(), &wg))
}

func TestWaitGroupWaitCompletesLater(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	wg.Add(1)

	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		<-release
		wg.Done()
	}()
	go func() {
		done <- WaitGroupWait(t.Context(), &wg)
	}()

	select {
	case err := <-done:
		require.Failf(t, "WaitGroupWait returned before wait group completed", "err: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("WaitGroupWait did not return after wait group completed")
	}
}

func TestWaitGroupWaitReturnsContextErrorWhileWaiting(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, WaitGroupWait(ctx, &wg), context.Canceled)
}

func TestWaitGroupWaitAlreadyDoneIgnoresCanceledContext(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, WaitGroupWait(ctx, &wg))
}

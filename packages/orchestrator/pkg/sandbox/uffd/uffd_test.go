//go:build linux

package uffd

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
)

// TestSetOnFailure verifies that the failure callback is properly set and can be invoked.
func TestSetOnFailure(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	// Verify callback is initially nil
	assert.Nil(t, u.GetOnFailure())

	// Set a callback
	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {
		// Callback set
	})

	// Verify callback is set
	assert.NotNil(t, u.GetOnFailure())
}

// TestOnFailureInvokedOnHandleError verifies that the failure callback is invoked when handle fails.
// This test verifies the callback mechanism by checking that it's called when handle returns an error.
func TestOnFailureInvokedOnHandleError(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	// Track callback invocation
	var (
		callbackInvoked atomic.Bool
		callbackSandbox string
		callbackErr     error
		mu              sync.Mutex
		wg              sync.WaitGroup
	)

	wg.Add(1)
	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {
		defer wg.Done()
		callbackInvoked.Store(true)
		mu.Lock()
		defer mu.Unlock()
		callbackSandbox = sandboxID
		callbackErr = err
	})

	// Start UFFD - it will fail because no Firecracker connects
	// The socket deadline is 10 seconds, so we need to wait for that
	ctx := context.Background()
	sandboxID := "test-sandbox-123"
	err := u.Start(ctx, sandboxID)
	require.NoError(t, err, "Start should not error immediately")

	// Wait for the goroutine to process the failure (socket timeout is 10 seconds)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Callback was invoked
	case <-time.After(15 * time.Second):
		t.Fatal("callback was not invoked within timeout")
	}

	// Verify callback was invoked
	assert.True(t, callbackInvoked.Load(), "callback should have been invoked")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, sandboxID, callbackSandbox, "callback should receive correct sandbox ID")
	assert.NotNil(t, callbackErr, "callback should receive error")
}

// TestOnFailureNotInvokedWhenCallbackNil verifies that nil callback doesn't crash.
func TestOnFailureNotInvokedWhenCallbackNil(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	// Don't set callback - should not crash
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := u.Start(ctx, "test-sandbox")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Should be able to stop without issues
	stopErr := u.Stop()
	assert.NoError(t, stopErr, "Stop should not error")
}

// TestMultipleCallbackSets verifies that SetOnFailure can be called multiple times.
func TestMultipleCallbackSets(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	// Set first callback
	firstInvoked := atomic.Bool{}
	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {
		firstInvoked.Store(true)
	})

	// Verify first callback is set
	assert.NotNil(t, u.GetOnFailure())

	// Set second callback (should replace first)
	secondInvoked := atomic.Bool{}
	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {
		secondInvoked.Store(true)
	})

	// Verify second callback is set
	assert.NotNil(t, u.GetOnFailure())

	// Note: We don't actually trigger the failure here because it would take 10 seconds
	// The important thing is that SetOnFailure can be called multiple times
}

// TestCallbackReceivesCorrectParameters verifies callback receives correct context, sandbox ID, and error.
// This is a long-running test that waits for the socket timeout (10 seconds).
func TestCallbackReceivesCorrectParameters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	var (
		receivedCtx     context.Context
		receivedSandbox string
		receivedErr     error
		mu              sync.Mutex
		wg              sync.WaitGroup
	)

	wg.Add(1)
	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {
		defer wg.Done()
		mu.Lock()
		defer mu.Unlock()
		receivedCtx = ctx
		receivedSandbox = sandboxID
		receivedErr = err
	})

	sandboxID := "test-sandbox-xyz"
	err := u.Start(context.Background(), sandboxID)
	require.NoError(t, err)

	// Wait for callback (socket timeout is 10 seconds)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("callback was not invoked within timeout")
	}

	mu.Lock()
	defer mu.Unlock()

	assert.NotNil(t, receivedCtx, "callback should receive context")
	assert.Equal(t, sandboxID, receivedSandbox, "callback should receive correct sandbox ID")
	assert.NotNil(t, receivedErr, "callback should receive error")
}

// TestUffdStopAfterFailure verifies that UFFD can be stopped after a failure.
func TestUffdStopAfterFailure(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {
		// Callback does nothing
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := u.Start(ctx, "test-sandbox")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Should be able to stop UFFD after failure
	stopErr := u.Stop()
	assert.NoError(t, stopErr, "Stop should not error after failure")
}

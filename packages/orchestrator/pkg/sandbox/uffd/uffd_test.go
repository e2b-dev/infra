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

// TestSetOnFailure verifies that the failure callback is properly set and can be retrieved.
func TestSetOnFailure(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	assert.Nil(t, u.GetOnFailure())

	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {})

	assert.NotNil(t, u.GetOnFailure())
}

// TestOnFailureNotInvokedWhenCallbackNil verifies that a nil callback doesn't crash.
func TestOnFailureNotInvokedWhenCallbackNil(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := u.Start(ctx, "test-sandbox")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	stopErr := u.Stop()
	assert.NoError(t, stopErr)
}

// TestMultipleCallbackSets verifies that SetOnFailure can be called multiple times,
// with each call replacing the previous callback.
func TestMultipleCallbackSets(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {})
	assert.NotNil(t, u.GetOnFailure())

	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {})
	assert.NotNil(t, u.GetOnFailure())

	u.SetOnFailure(nil)
	assert.Nil(t, u.GetOnFailure())
}

// TestUffdStopAfterFailure verifies that UFFD can be stopped cleanly after a failure.
func TestUffdStopAfterFailure(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)
	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := u.Start(ctx, "test-sandbox")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	stopErr := u.Stop()
	assert.NoError(t, stopErr)
}

// TestCallbackNotInvokedOnCleanStop verifies that the callback is NOT invoked
// when UFFD is stopped cleanly (no failure).
func TestCallbackNotInvokedOnCleanStop(t *testing.T) {
	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	invoked := atomic.Bool{}
	u.SetOnFailure(func(ctx context.Context, sandboxID string, err error) {
		invoked.Store(true)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := u.Start(ctx, "test-sandbox")
	require.NoError(t, err)

	_ = u.Stop()

	time.Sleep(50 * time.Millisecond)

	assert.False(t, invoked.Load(), "callback should not be invoked on clean stop")
}

// TestOnFailureInvokedOnHandleError verifies that the failure callback is invoked
// when handle fails (socket timeout). Long-running: waits ~10s for socket deadline.
func TestOnFailureInvokedOnHandleError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	var (
		wg              sync.WaitGroup
		callbackSandbox string
		callbackErr     error
		mu              sync.Mutex
	)

	wg.Add(1)
	sandboxID := "test-sandbox-123"
	u.SetOnFailure(func(ctx context.Context, sbxID string, err error) {
		defer wg.Done()
		mu.Lock()
		defer mu.Unlock()
		callbackSandbox = sbxID
		callbackErr = err
	})

	err := u.Start(context.Background(), sandboxID)
	require.NoError(t, err)

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
	assert.Equal(t, sandboxID, callbackSandbox)
	assert.NotNil(t, callbackErr)
}

// TestLateCallbackRegistrationAfterFailure verifies the key correctness guarantee
// from the code review: a callback registered AFTER a failure has already occurred
// is still invoked immediately with the stored failure details.
func TestLateCallbackRegistrationAfterFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running test in short mode")
	}

	t.Parallel()

	memfile := testutils.NewMockMemfile(t)
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	u := New(memfile, socketPath)

	sandboxID := "test-sandbox-late"

	// Start without a callback - UFFD will fail after socket timeout (10 seconds)
	err := u.Start(context.Background(), sandboxID)
	require.NoError(t, err)

	// Wait for the internal goroutine to finish (socket timeout = 10 seconds)
	<-u.readyCh

	// Register callback AFTER failure has already occurred.
	// It must be invoked immediately in a goroutine with the stored failure details.
	var (
		wg              sync.WaitGroup
		receivedSandbox string
		receivedErr     error
		mu              sync.Mutex
	)

	wg.Add(1)
	u.SetOnFailure(func(ctx context.Context, sbxID string, cbErr error) {
		defer wg.Done()
		mu.Lock()
		defer mu.Unlock()
		receivedSandbox = sbxID
		receivedErr = cbErr
	})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("late-registered callback was not invoked")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, sandboxID, receivedSandbox)
	assert.NotNil(t, receivedErr)
}

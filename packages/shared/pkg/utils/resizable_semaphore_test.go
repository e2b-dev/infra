package utils

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: make sure f panics.
func mustPanic(t *testing.T) {
	if r := recover(); r == nil {
		t.Fatalf("expected panic but none occurred")
	}
}

// -----------------------------------------------------------------------------
// basic correctness
// -----------------------------------------------------------------------------

func TestBasicAcquireTryRelease(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	err = s.Acquire(t.Context(), 1)
	require.NoError(t, err)
	got := s.TryAcquire(1)
	require.True(t, got, "TryAcquire should have succeeded with remaining capacity")
	got = s.TryAcquire(1)
	require.False(t, got, "TryAcquire should have failed (limit exceeded)")
	s.Release(2) // returns everything
	got = s.TryAcquire(2)
	require.True(t, got, "TryAcquire should succeed after Release")
}

// -----------------------------------------------------------------------------
// Acquire with limit changes
// -----------------------------------------------------------------------------
func TestAcquireWithLimitIncrease(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	err = s.Acquire(t.Context(), 1)
	require.NoError(t, err)

	// Try to acquire 2, should block
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := s.Acquire(context.Background(), 2)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond) // ensure goroutine is blocked

	// Increase limit to allow the second acquire
	s.SetLimit(3)

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Acquire did not unblock after SetLimit")
	}
}

func TestAcquireWithLimitDecrease(t *testing.T) {
	s, err := NewAdjustableSemaphore(3)
	require.NoError(t, err)

	err = s.Acquire(t.Context(), 2)
	require.NoError(t, err)

	// Try to acquire 2 more, should block
	done := make(chan struct{})
	go func() {
		defer close(done)
		err = s.Acquire(t.Context(), 2)
		require.NoError(t, err)
	}()

	time.Sleep(50 * time.Millisecond) // ensure goroutine is blocked

	// Decrease limit to below current usage
	s.SetLimit(2)

	select {
	case <-done:
		t.Fatal("Acquire should not have unblocked after SetLimit decrease")
	case <-time.After(50 * time.Millisecond):
	}

	s.Release(2)
	// Now it should succeed since we released enough
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Acquire did not unblock after Release")
	}
}

// -----------------------------------------------------------------------------
// error handling
// -----------------------------------------------------------------------------
func TestAcquireErrorsOnNegativeN(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	err = s.Acquire(t.Context(), -1) // should fail
	if err == nil {
		t.Fatalf("expected error on negative n, got nil")
	}
}

func TestAcquireErrorsOnZeroN(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	err = s.Acquire(t.Context(), 0) // should fail
	if err == nil {
		t.Fatalf("expected error on zero n, got nil")
	}
}

func TestReleaseErrorsOnNegativeN(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	defer mustPanic(t)
	s.Release(-1)
}

func TestReleaseMoreThanAcquired(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	// should panic on negative release
	defer mustPanic(t)
	s.Release(2)
}

func TestReleaseErrorsOnZero(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	defer mustPanic(t)
	s.Release(0)
}

func TestSetLimitErrorsOnNegativeLimit(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	err = s.SetLimit(-1)
	assert.Error(t, err, "SetLimit should return an error for negative limit")
}

func TestSetLimitErrorsOnZeroLimit(t *testing.T) {
	s, err := NewAdjustableSemaphore(2)
	require.NoError(t, err)

	err = s.SetLimit(0)
	assert.Error(t, err, "SetLimit should return an error for zero limit")
}

func TestNewAdjustableSemaphoreErrorsOnNegativeLimit(t *testing.T) {
	_, err := NewAdjustableSemaphore(-1)
	assert.Error(t, err, "NewAdjustableSemaphore should return an error for negative limit")
}

func TestNewAdjustableSemaphoreErrorsOnZeroLimit(t *testing.T) {
	_, err := NewAdjustableSemaphore(0)
	assert.Error(t, err, "NewAdjustableSemaphore should return an error for zero limit")
}

// -----------------------------------------------------------------------------
// blocking behaviour and SetLimit
// -----------------------------------------------------------------------------

func TestAcquireBlocksUntilRelease(t *testing.T) {
	s, err := NewAdjustableSemaphore(1)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := s.Acquire(ctx, 1); err != nil {
		t.Fatalf("initial acquire failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		// will block until Release below
		_ = s.Acquire(ctx, 1)
		close(done)
	}()

	// give goroutine time to park
	time.Sleep(20 * time.Millisecond)
	s.Release(1)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("Acquire did not unblock after Release")
	}
}

func TestAcquireUnblocksOnSetLimit(t *testing.T) {
	s, err := NewAdjustableSemaphore(1)
	require.NoError(t, err)
	if err := s.Acquire(context.Background(), 1); err != nil {
		t.Fatalf("initial acquire failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = s.Acquire(context.Background(), 1) // waits
		close(done)
	}()

	time.Sleep(10 * time.Millisecond) // ensure waiter is parked
	s.SetLimit(2)                     // enlarges limit

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("Acquire did not unblock after SetLimit")
	}
}

// -----------------------------------------------------------------------------
// context cancellation
// -----------------------------------------------------------------------------

func TestAcquireRespectsContextCancel(t *testing.T) {
	s, err := NewAdjustableSemaphore(1)
	require.NoError(t, err)

	_ = s.Acquire(context.Background(), 1)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	go func() { errCh <- s.Acquire(ctx, 1) }()

	time.Sleep(10 * time.Millisecond) // let it block
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("Acquire didn’t return after context cancellation")
	}
}

// -----------------------------------------------------------------------------
// race-detector stress
// -----------------------------------------------------------------------------

func TestConcurrentStressNoDeadlockOrRace(t *testing.T) {
	const (
		gor        = 20
		iterations = 1_000
	)
	s, err := NewAdjustableSemaphore(5)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(gor)
	for i := 0; i < gor; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = s.Acquire(context.Background(), 1)
				// tiny critical-section
				s.Release(1)
			}
		}()
	}
	wg.Wait()

	// yield to the scheduler to let any stale callbacks run
	runtime.Gosched()
}

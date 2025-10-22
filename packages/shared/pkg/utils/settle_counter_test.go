package utils

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSettleCounter_NewZeroSettleCounter(t *testing.T) {
	sc := NewZeroSettleCounter()

	// Counter should start at 0 (settle value)
	require.Equal(t, int64(0), sc.counter, "Expected counter to start at 0")
}

func TestSettleCounter_AddAndDone(t *testing.T) {
	sc := NewZeroSettleCounter()

	// Test Add
	sc.Add()
	require.Equal(t, int64(1), sc.counter, "Expected counter to be 1 after Add")

	sc.Add()
	require.Equal(t, int64(2), sc.counter, "Expected counter to be 2 after second Add")

	// Test Done
	sc.Done()
	require.Equal(t, int64(1), sc.counter, "Expected counter to be 1 after Done")

	sc.Done()
	require.Equal(t, int64(0), sc.counter, "Expected counter to be 0 after second Done")
}

func TestSettleCounter_Wait_AlreadySettled(t *testing.T) {
	sc := NewZeroSettleCounter()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Counter is already at 0, should return immediately
	err := sc.Wait(ctx)
	require.NoError(t, err, "Expected no error when already settled")
}

func TestSettleCounter_Wait_SettlesAfterDone(t *testing.T) {
	sc := NewZeroSettleCounter()

	// Add some work
	sc.Add()
	sc.Add()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Start waiting in a goroutine
	var wg sync.WaitGroup
	var waitErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitErr = sc.Wait(ctx)
	}()

	// Give the wait goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Complete the work
	sc.Done()
	sc.Done()

	// Wait for the wait to complete
	wg.Wait()

	require.NoError(t, waitErr, "Expected no error when settling")
}

func TestSettleCounter_Wait_ContextTimeout(t *testing.T) {
	sc := NewZeroSettleCounter()

	// Add work that won't be completed
	sc.Add()
	sc.Add()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := sc.Wait(ctx)
	require.Error(t, err, "Expected context timeout error")
	require.ErrorIs(t, err, context.DeadlineExceeded, "Expected context.DeadlineExceeded")
}

func TestSettleCounter_Wait_ContextCancel(t *testing.T) {
	sc := NewZeroSettleCounter()

	// Add work that won't be completed
	sc.Add()
	sc.Add()

	ctx, cancel := context.WithCancel(context.Background())

	// Start waiting in a goroutine
	var wg sync.WaitGroup
	var waitErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitErr = sc.Wait(ctx)
	}()

	// Give the wait goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for the wait to complete
	wg.Wait()

	require.Error(t, waitErr, "Expected context cancellation error")

	require.ErrorIs(t, waitErr, context.Canceled, "Expected context.Canceled")
}

func TestSettleCounter_Close(t *testing.T) {
	sc := NewZeroSettleCounter()

	// Add some work
	sc.Add()
	sc.Add()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Start waiting in a goroutine
	var wg sync.WaitGroup
	var waitErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitErr = sc.Wait(ctx)
	}()

	// Give the wait goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Close should settle the counter
	sc.close()

	// Wait for the wait to complete
	wg.Wait()

	require.NoError(t, waitErr, "Expected no error when closing")

	// Counter should be at settle value (0)
	require.Equal(t, int64(0), sc.counter, "Expected counter to be 0 after Close")
}

func TestSettleCounter_ConcurrentOperations(t *testing.T) {
	sc := NewZeroSettleCounter()

	const numGoroutines = 10
	const operationsPerGoroutine = 100

	_, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	// Start multiple goroutines that add and then done
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range operationsPerGoroutine {
				sc.Add()
				sc.Done()
			}
		}()
	}

	// Wait for all operations to complete
	wg.Wait()

	// Counter should be back to 0
	require.Equal(t, int64(0), sc.counter, "Expected counter to be 0 after all operations")
}

func TestSettleCounter_MultipleWaiters(t *testing.T) {
	sc := NewZeroSettleCounter()

	// Add some work
	sc.Add()
	sc.Add()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	const numWaiters = 5
	var wg sync.WaitGroup
	errors := make([]error, numWaiters)

	// Start multiple waiters
	for i := range numWaiters {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errors[index] = sc.Wait(ctx)
		}(i)
	}

	// Give waiters time to start
	time.Sleep(10 * time.Millisecond)

	// Complete the work
	sc.Done()
	sc.Done()

	// Wait for all waiters to complete
	wg.Wait()

	// All waiters should succeed
	for i, err := range errors {
		require.NoError(t, err, "Waiter %d got unexpected error: %v", i, err)
	}
}

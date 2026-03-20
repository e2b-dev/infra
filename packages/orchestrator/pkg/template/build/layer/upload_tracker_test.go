package layer

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadTracker_SingleUpload(t *testing.T) {
	t.Parallel()

	tracker := NewUploadTracker()

	complete, waitForPrevious := tracker.StartUpload()

	// First upload has no previous uploads to wait for
	ctx := context.Background()
	err := waitForPrevious(ctx)
	require.NoError(t, err)

	complete()
}

func TestUploadTracker_SequentialUploads(t *testing.T) {
	t.Parallel()

	tracker := NewUploadTracker()

	// Start first upload
	complete1, waitForPrevious1 := tracker.StartUpload()

	// Start second upload
	complete2, waitForPrevious2 := tracker.StartUpload()

	// Start third upload
	complete3, waitForPrevious3 := tracker.StartUpload()

	ctx := context.Background()

	// First upload has no dependencies
	err := waitForPrevious1(ctx)
	require.NoError(t, err)
	complete1()

	// Second upload waits for first
	err = waitForPrevious2(ctx)
	require.NoError(t, err)
	complete2()

	// Third upload waits for first and second
	err = waitForPrevious3(ctx)
	require.NoError(t, err)
	complete3()
}

func TestUploadTracker_WaitBlocksUntilComplete(t *testing.T) {
	t.Parallel()

	tracker := NewUploadTracker()

	// Start first upload
	complete1, _ := tracker.StartUpload()

	// Start second upload
	_, waitForPrevious2 := tracker.StartUpload()

	// Second upload should block until first completes
	done := make(chan struct{})
	go func() {
		ctx := context.Background()
		_ = waitForPrevious2(ctx)
		close(done)
	}()

	// Should not complete immediately
	select {
	case <-done:
		t.Fatal("waitForPrevious should have blocked")
	case <-time.After(50 * time.Millisecond):
		// Expected - still waiting
	}

	// Complete first upload
	complete1()

	// Now second should complete
	select {
	case <-done:
		// Expected
	case <-time.After(time.Second):
		t.Fatal("waitForPrevious should have completed after first upload finished")
	}
}

func TestUploadTracker_ContextCancellation(t *testing.T) {
	t.Parallel()

	tracker := NewUploadTracker()

	// Start first upload (don't complete it)
	_, _ = tracker.StartUpload()

	// Start second upload
	_, waitForPrevious2 := tracker.StartUpload()

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start waiting in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- waitForPrevious2(ctx)
	}()

	// Cancel the context
	cancel()

	// Should return context error
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("waitForPrevious should have returned after context cancellation")
	}
}

func TestUploadTracker_ConcurrentUploads(t *testing.T) {
	t.Parallel()

	tracker := NewUploadTracker()

	const numUploads = 10
	var completeFuncs []func()
	var waitFuncs []func(context.Context) error

	// Start all uploads
	for range numUploads {
		complete, wait := tracker.StartUpload()
		completeFuncs = append(completeFuncs, complete)
		waitFuncs = append(waitFuncs, wait)
	}

	// Track completion order and errors
	var completionOrder []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, numUploads)

	// Start all waits concurrently
	for i := range numUploads {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := context.Background()
			err := waitFuncs[idx](ctx)
			if err != nil {
				errCh <- err

				return
			}

			mu.Lock()
			completionOrder = append(completionOrder, idx)
			mu.Unlock()
		}(i)
	}

	// Complete uploads in order
	for i := range numUploads {
		completeFuncs[i]()
		// Small delay to allow goroutines to process
		time.Sleep(10 * time.Millisecond)
	}

	wg.Wait()
	close(errCh)

	// Check for errors
	for err := range errCh {
		require.NoError(t, err)
	}

	// Verify all completed
	assert.Len(t, completionOrder, numUploads)
}

func TestUploadTracker_OutOfOrderCompletion(t *testing.T) {
	t.Parallel()

	tracker := NewUploadTracker()

	// Start three uploads
	complete1, waitForPrevious1 := tracker.StartUpload()
	complete2, waitForPrevious2 := tracker.StartUpload()
	complete3, waitForPrevious3 := tracker.StartUpload()

	ctx := context.Background()

	// Track when each wait completes
	var wait1Done, wait2Done, wait3Done atomic.Bool

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		_ = waitForPrevious1(ctx)
		wait1Done.Store(true)
	}()

	go func() {
		defer wg.Done()
		_ = waitForPrevious2(ctx)
		wait2Done.Store(true)
	}()

	go func() {
		defer wg.Done()
		_ = waitForPrevious3(ctx)
		wait3Done.Store(true)
	}()

	// Wait 1 should complete immediately (no dependencies)
	time.Sleep(50 * time.Millisecond)
	assert.True(t, wait1Done.Load(), "wait1 should complete immediately")
	assert.False(t, wait2Done.Load(), "wait2 should still be waiting")
	assert.False(t, wait3Done.Load(), "wait3 should still be waiting")

	// Complete upload 1
	complete1()
	time.Sleep(50 * time.Millisecond)

	// Wait 2 should now complete
	assert.True(t, wait2Done.Load(), "wait2 should complete after upload1")
	assert.False(t, wait3Done.Load(), "wait3 should still be waiting for upload2")

	// Complete upload 2
	complete2()
	time.Sleep(50 * time.Millisecond)

	// Wait 3 should now complete
	assert.True(t, wait3Done.Load(), "wait3 should complete after upload2")

	// Complete upload 3 for cleanup
	complete3()

	wg.Wait()
}

func TestUploadTracker_CompleteBeforeWait(t *testing.T) {
	t.Parallel()

	tracker := NewUploadTracker()

	// Start and complete first upload before second even starts waiting
	complete1, _ := tracker.StartUpload()
	complete1()

	// Start second upload
	_, waitForPrevious2 := tracker.StartUpload()

	// Should not block since first is already complete
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		_ = waitForPrevious2(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Expected - should complete immediately
	case <-time.After(time.Second):
		t.Fatal("waitForPrevious should have completed immediately since previous upload is done")
	}
}

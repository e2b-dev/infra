package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests use an empty Orchestrator value — only the bgWg field is needed.

func TestGoBackgroundTracksGoroutine(t *testing.T) {
	t.Parallel()

	var o Orchestrator
	var done atomic.Bool

	o.GoBackground(func() {
		time.Sleep(30 * time.Millisecond)
		done.Store(true)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	finished := o.WaitBackground(ctx)
	require.True(t, finished, "WaitBackground should return true when goroutine finishes")
	assert.True(t, done.Load(), "goroutine should have completed")
}

func TestWaitBackgroundTimesOutOnSlow(t *testing.T) {
	t.Parallel()

	var o Orchestrator

	blocker := make(chan struct{})
	o.GoBackground(func() { <-blocker })
	t.Cleanup(func() { close(blocker) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	finished := o.WaitBackground(ctx)
	assert.False(t, finished, "WaitBackground should return false when ctx expires first")
}

func TestGoBackgroundMultipleGoroutines(t *testing.T) {
	t.Parallel()

	var o Orchestrator
	const n = 20
	var count atomic.Int32

	for i := 0; i < n; i++ {
		o.GoBackground(func() {
			time.Sleep(10 * time.Millisecond)
			count.Add(1)
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.True(t, o.WaitBackground(ctx))
	assert.Equal(t, int32(n), count.Load())
}

func TestWaitBackgroundNoGoroutinesReturnsImmediately(t *testing.T) {
	t.Parallel()

	var o Orchestrator

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	finished := o.WaitBackground(ctx)
	elapsed := time.Since(start)

	require.True(t, finished)
	assert.Less(t, elapsed, 50*time.Millisecond, "no goroutines pending — should be instant")
}

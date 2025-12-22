package utils

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWait(t *testing.T) {
	t.Run("can early exit on pre-canceled context", func(t *testing.T) {
		var wg sync.WaitGroup

		wg.Add(1)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		start := time.Now()
		err := Wait(ctx, &wg)
		actualFinish := time.Now()

		require.ErrorIs(t, err, context.Canceled)
		assert.WithinDuration(t, start, actualFinish, 10*time.Millisecond)
	})

	t.Run("can wait for context cancellation", func(t *testing.T) {
		var wg sync.WaitGroup

		wg.Add(1)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		sleepTime := 50 * time.Millisecond
		go func() {
			time.Sleep(sleepTime)
			cancel()
		}()

		start := time.Now()
		err := Wait(ctx, &wg)
		actualFinish := time.Now()
		expectedFinish := start.Add(sleepTime)

		require.ErrorIs(t, err, context.Canceled)
		assert.WithinDuration(t, expectedFinish, actualFinish, 10*time.Millisecond)
	})

	t.Run("can wait for context timeout", func(t *testing.T) {
		var wg sync.WaitGroup

		wg.Add(1)

		sleepTime := 50 * time.Millisecond
		ctx, cancel := context.WithTimeout(t.Context(), sleepTime)
		t.Cleanup(cancel)

		start := time.Now()
		err := Wait(ctx, &wg)
		actualFinish := time.Now()
		expectedFinish := start.Add(sleepTime)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.WithinDuration(t, expectedFinish, actualFinish, 10*time.Millisecond)
	})

	t.Run("can wait for wait group", func(t *testing.T) {
		var wg sync.WaitGroup

		wg.Add(1)

		sleepTime := 50 * time.Millisecond
		go func() {
			time.Sleep(sleepTime)
			wg.Done()
		}()

		start := time.Now()
		err := Wait(t.Context(), &wg)
		actualFinish := time.Now()
		expectedFinish := start.Add(sleepTime)

		require.NoError(t, err)
		assert.WithinDuration(t, expectedFinish, actualFinish, 10*time.Millisecond)
	})
}

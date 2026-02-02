package tcpfirewall

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionLimiter_TryAcquire(t *testing.T) {
	t.Parallel()

	t.Run("acquires up to limit", func(t *testing.T) {
		t.Parallel()
		limiter := NewConnectionLimiter()

		count, ok := limiter.TryAcquire("sandbox1", 3)
		assert.True(t, ok)
		assert.Equal(t, int64(1), count)

		count, ok = limiter.TryAcquire("sandbox1", 3)
		assert.True(t, ok)
		assert.Equal(t, int64(2), count)

		count, ok = limiter.TryAcquire("sandbox1", 3)
		assert.True(t, ok)
		assert.Equal(t, int64(3), count)
	})

	t.Run("rejects when at limit", func(t *testing.T) {
		t.Parallel()
		limiter := NewConnectionLimiter()

		for range 3 {
			_, ok := limiter.TryAcquire("sandbox1", 3)
			require.True(t, ok)
		}

		count, ok := limiter.TryAcquire("sandbox1", 3)
		assert.False(t, ok)
		assert.Equal(t, int64(3), count)
	})

	t.Run("zero limit means no limit", func(t *testing.T) {
		t.Parallel()
		limiter := NewConnectionLimiter()

		for i := 1; i <= 100; i++ {
			count, ok := limiter.TryAcquire("sandbox1", 0)
			assert.True(t, ok)
			assert.Equal(t, int64(i), count)
		}
	})

	t.Run("negative limit means no limit", func(t *testing.T) {
		t.Parallel()
		limiter := NewConnectionLimiter()

		for i := 1; i <= 10; i++ {
			count, ok := limiter.TryAcquire("sandbox1", -1)
			assert.True(t, ok)
			assert.Equal(t, int64(i), count)
		}
	})

	t.Run("separate sandboxes have separate limits", func(t *testing.T) {
		t.Parallel()
		limiter := NewConnectionLimiter()

		_, ok := limiter.TryAcquire("sandbox1", 1)
		assert.True(t, ok)

		_, ok = limiter.TryAcquire("sandbox1", 1)
		assert.False(t, ok)

		_, ok = limiter.TryAcquire("sandbox2", 1)
		assert.True(t, ok)

		_, ok = limiter.TryAcquire("sandbox2", 1)
		assert.False(t, ok)
	})
}

func TestConnectionLimiter_Release(t *testing.T) {
	t.Parallel()

	t.Run("release allows new acquire", func(t *testing.T) {
		t.Parallel()
		limiter := NewConnectionLimiter()

		_, ok := limiter.TryAcquire("sandbox1", 1)
		require.True(t, ok)

		_, ok = limiter.TryAcquire("sandbox1", 1)
		assert.False(t, ok)

		limiter.Release("sandbox1")

		_, ok = limiter.TryAcquire("sandbox1", 1)
		assert.True(t, ok)
	})

	t.Run("cleanup when count reaches zero", func(t *testing.T) {
		t.Parallel()
		limiter := NewConnectionLimiter()

		_, ok := limiter.TryAcquire("sandbox1", 10)
		require.True(t, ok)

		_, exists := limiter.connections.Load("sandbox1")
		assert.True(t, exists)

		limiter.Release("sandbox1")

		_, exists = limiter.connections.Load("sandbox1")
		assert.False(t, exists, "entry should be cleaned up when count reaches 0")
	})
}

func TestConnectionLimiter_Count(t *testing.T) {
	t.Parallel()

	limiter := NewConnectionLimiter()

	assert.Equal(t, int64(0), limiter.Count("sandbox1"))

	limiter.TryAcquire("sandbox1", 10)
	assert.Equal(t, int64(1), limiter.Count("sandbox1"))

	limiter.TryAcquire("sandbox1", 10)
	assert.Equal(t, int64(2), limiter.Count("sandbox1"))

	limiter.Release("sandbox1")
	assert.Equal(t, int64(1), limiter.Count("sandbox1"))
}

func TestConnectionLimiter_Concurrent(t *testing.T) {
	t.Parallel()

	limiter := NewConnectionLimiter()
	const limit = 50
	const goroutines = 100

	var wg sync.WaitGroup
	acquired := make(chan bool, goroutines)

	for range goroutines {
		wg.Go(func() {
			_, ok := limiter.TryAcquire("sandbox1", limit)
			acquired <- ok
		})
	}

	wg.Wait()
	close(acquired)

	successCount := 0
	for ok := range acquired {
		if ok {
			successCount++
		}
	}

	assert.Equal(t, limit, successCount, "exactly %d goroutines should succeed", limit)
	assert.Equal(t, int64(limit), limiter.Count("sandbox1"))
}

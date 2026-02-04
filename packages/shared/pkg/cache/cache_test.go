package cache

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestNewCache(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
		RefreshTimeout:  30 * time.Second,
	}

	cache := NewCache[string, string](config)
	require.NotNil(t, cache)
	assert.Equal(t, config.TTL, cache.config.TTL)
	assert.Equal(t, config.RefreshInterval, cache.config.RefreshInterval)
	assert.Equal(t, 30*time.Second, cache.config.RefreshTimeout)
}

func TestNewCache_DefaultRefreshTimeout(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
	}

	cache := NewCache[string, string](config)
	require.NotNil(t, cache)
	assert.Equal(t, 30*time.Second, cache.config.RefreshTimeout)
}

func TestCache_SetAndGet(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
	}
	cache := NewCache[string, string](config)

	t.Run("set and get value", func(t *testing.T) {
		t.Parallel()
		cache.Set("key1", "value1")
		value, found := cache.Get("key1")
		assert.True(t, found)
		assert.Equal(t, "value1", value)
	})

	t.Run("get non-existent key", func(t *testing.T) {
		t.Parallel()
		value, found := cache.Get("non-existent")
		assert.False(t, found)
		assert.Empty(t, value) // zero value for string
	})

	t.Run("overwrite existing value", func(t *testing.T) {
		t.Parallel()
		cache.Set("key2", "original")
		cache.Set("key2", "updated")
		value, found := cache.Get("key2")
		assert.True(t, found)
		assert.Equal(t, "updated", value)
	})
}

func TestCache_SetWithTTL(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL: 100 * time.Millisecond,
	}
	cache := NewCache[string, string](config)

	cache.Set("key1", "value1")

	// Value should be present immediately
	value, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, "value1", value)

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Value should be gone
	_, found = cache.Get("key1")
	assert.False(t, found)
}

func TestCache_Delete(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
	}
	cache := NewCache[string, string](config)

	cache.Set("key1", "value1")

	// Verify it exists
	value, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, "value1", value)

	// Delete it
	cache.Delete("key1")

	// Verify it's gone
	_, found = cache.Get("key1")
	assert.False(t, found)
}

func TestCache_GetOrSet_CacheMiss(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
	}
	cache := NewCache[string, string](config)

	callCount := 0
	callback := func(_ context.Context, key string) (string, error) {
		callCount++

		return fmt.Sprintf("value-%s", key), nil
	}

	value, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, "value-key1", value)
	assert.Equal(t, 1, callCount)

	// Verify it's now in cache
	cachedValue, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, "value-key1", cachedValue)
	assert.Equal(t, 1, callCount)
}

func TestCache_GetOrSet_CacheHit(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 0, // No refresh
	}
	cache := NewCache[string, string](config)

	callCount := 0
	callback := func(_ context.Context, key string) (string, error) {
		callCount++

		return fmt.Sprintf("value-%s", key), nil
	}

	// First call - cache miss
	value1, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, "value-key1", value1)
	assert.Equal(t, 1, callCount)

	// Second call - cache hit
	value2, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, "value-key1", value2)
	assert.Equal(t, 1, callCount) // Callback should not be called again
}

func TestCache_GetOrSet_CallbackError(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
	}
	cache := NewCache[string, string](config)

	expectedErr := errors.New("callback error")
	callback := func(_ context.Context, _ string) (string, error) {
		return "", expectedErr
	}

	value, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "callback error")
	assert.Empty(t, value)

	// Verify nothing was cached
	_, found := cache.Get("key1")
	assert.False(t, found)
}

func TestCache_GetOrSet_WithRefreshInterval(t *testing.T) {
	t.Parallel()
	config := Config[string, int]{
		TTL:             5 * time.Second,
		RefreshInterval: 50 * time.Millisecond,
		RefreshTimeout:  1 * time.Second,
	}
	cache := NewCache[string, int](config)

	var callCount atomic.Int32
	callback := func(_ context.Context, _ string) (int, error) {
		count := int(callCount.Add(1))

		return count, nil
	}

	// Initial call - cache miss
	value1, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, 1, value1)
	assert.Equal(t, int32(1), callCount.Load())

	// Immediate second call - cache hit, no refresh yet
	value2, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, 1, value2) // Still returns original value
	assert.Equal(t, int32(1), callCount.Load())

	// Wait for refresh interval to pass
	time.Sleep(100 * time.Millisecond)

	// This call should trigger background refresh but still return stale value
	value3, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, 1, value3) // Still returns stale value immediately

	// Wait for background refresh to complete
	time.Sleep(200 * time.Millisecond)

	// Now the cache should have the refreshed value
	value4, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, 2, value4) // Updated value from refresh
	assert.Equal(t, int32(2), callCount.Load())
}

func TestCache_GetOrSet_RefreshOnlyOnce(t *testing.T) {
	t.Parallel()
	config := Config[string, int]{
		TTL:             5 * time.Second,
		RefreshInterval: 50 * time.Millisecond,
		RefreshTimeout:  1 * time.Second,
	}
	cache := NewCache[string, int](config)

	var callCount atomic.Int32
	callback := func(_ context.Context, _ string) (int, error) {
		time.Sleep(100 * time.Millisecond) // Simulate slow callback
		count := int(callCount.Add(1))

		return count, nil
	}

	// Initial call
	value1, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, 1, value1)

	// Wait for refresh interval
	time.Sleep(100 * time.Millisecond)

	// Multiple concurrent calls should only trigger one refresh
	var wg errgroup.Group
	results := make([]int, 10)
	for i := range 10 {
		wg.Go(func() error {
			value, err := cache.GetOrSet(context.Background(), "key1", callback)
			if err != nil {
				return err
			}
			results[i] = value

			return nil
		})
	}

	err = wg.Wait()
	require.NoError(t, err)

	// All should return the stale value (1) immediately
	for i, result := range results {
		assert.Equal(t, 1, result, "result %d should be 1", i)
	}

	// Wait for refresh to complete
	time.Sleep(200 * time.Millisecond)

	// Verify only one refresh happened (callCount should be 2: initial + 1 refresh)
	assert.LessOrEqual(t, callCount.Load(), int32(2))
}

func TestCache_Refresh_DeletesOnError(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Second,
		RefreshInterval: 50 * time.Millisecond,
		RefreshTimeout:  1 * time.Second,
	}
	cache := NewCache[string, string](config)

	var shouldFail atomic.Bool
	shouldFail.Store(false)

	callback := func(_ context.Context, _ string) (string, error) {
		if shouldFail.Load() {
			return "", errors.New("refresh error")
		}

		return "success", nil
	}

	// Initial successful call
	value, err := cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err)
	assert.Equal(t, "success", value)

	// Verify it's cached
	_, found := cache.Get("key1")
	assert.True(t, found)

	// Wait for refresh interval
	time.Sleep(100 * time.Millisecond)

	// Make callback fail
	shouldFail.Store(true)

	// Trigger refresh
	_, err = cache.GetOrSet(context.Background(), "key1", callback)
	require.NoError(t, err) // GetOrSet returns the stale value

	// Wait for refresh to complete
	time.Sleep(200 * time.Millisecond)

	// Cache should be cleared due to refresh error
	_, found = cache.Get("key1")
	assert.False(t, found)
}

func TestCache_ContextCancellation(t *testing.T) {
	t.Parallel()
	config := Config[string, string]{
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
	}
	cache := NewCache[string, string](config)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	callback := func(ctx context.Context, _ string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return "value", nil
		}
	}

	_, err := cache.GetOrSet(ctx, "key1", callback)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCache_ExtractKeyFunc(t *testing.T) {
	t.Parallel()
	type User struct {
		ID   string
		Name string
	}

	t.Run("extract key from value on cache miss", func(t *testing.T) {
		t.Parallel()
		config := Config[string, User]{
			TTL:             5 * time.Minute,
			RefreshInterval: 0,
			ExtractKeyFunc: func(value User) string {
				return value.ID
			},
		}
		cache := NewCache[string, User](config)

		callback := func(_ context.Context, _ string) (User, error) {
			return User{ID: "user-123", Name: "Alice"}, nil
		}

		// Call with a different key, but ExtractKeyFunc should use the ID from the value
		value, err := cache.GetOrSet(context.Background(), "any-key", callback)
		require.NoError(t, err)
		assert.Equal(t, "user-123", value.ID)
		assert.Equal(t, "Alice", value.Name)

		// Verify the value is cached under the extracted key, not the original key
		cachedValue, found := cache.Get("user-123")
		assert.True(t, found)
		assert.Equal(t, "Alice", cachedValue.Name)

		// Original key should not have the value
		_, found = cache.Get("any-key")
		assert.False(t, found)
	})

	t.Run("extract key without ExtractKeyFunc", func(t *testing.T) {
		t.Parallel()
		config := Config[string, User]{
			TTL:             5 * time.Minute,
			RefreshInterval: 0,
		}
		cache := NewCache[string, User](config)

		callback := func(_ context.Context, _ string) (User, error) {
			return User{ID: "user-456", Name: "Bob"}, nil
		}

		// Without ExtractKeyFunc, should use the provided key
		value, err := cache.GetOrSet(context.Background(), "custom-key", callback)
		require.NoError(t, err)
		assert.Equal(t, "user-456", value.ID)
		assert.Equal(t, "Bob", value.Name)

		// Should be cached under the provided key
		cachedValue, found := cache.Get("custom-key")
		assert.True(t, found)
		assert.Equal(t, "Bob", cachedValue.Name)

		// Should not be under the extracted ID
		_, found = cache.Get("user-456")
		assert.False(t, found)
	})
}

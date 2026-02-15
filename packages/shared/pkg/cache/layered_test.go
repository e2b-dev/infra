package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

type testValue struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newTestLayeredCache(t *testing.T, redisClient redis.UniversalClient) *LayeredCache[testValue] {
	t.Helper()

	return NewLayeredCache[testValue](LayeredConfig[testValue]{
		L1TTL:       5 * time.Second,
		RedisTTL:    30 * time.Second,
		RedisClient: redisClient,
		RedisPrefix: fmt.Sprintf("test:%s", t.Name()),
	})
}

func TestLayeredCache_L1Hit(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	key := "key1"
	expected := testValue{ID: "1", Name: "Alice"}

	// Store directly in L1
	lc.l1.Set(key, expected)

	callbackCalled := false
	result, err := lc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callbackCalled = true

		return testValue{}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.False(t, callbackCalled, "callback should not be called on L1 hit")
}

func TestLayeredCache_L2Hit(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	key := "key1"
	expected := testValue{ID: "2", Name: "Bob"}

	// Store directly in Redis
	data, err := json.Marshal(expected)
	require.NoError(t, err)
	err = redisClient.Set(t.Context(), lc.RedisKey(key), data, 30*time.Second).Err()
	require.NoError(t, err)

	callbackCalled := false
	result, err := lc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callbackCalled = true

		return testValue{}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.False(t, callbackCalled, "callback should not be called on L2 hit")

	// Verify L1 was populated
	l1Value, found := lc.GetWithoutTouch(key)
	assert.True(t, found)
	assert.Equal(t, expected, l1Value)
}

func TestLayeredCache_L3Fallback(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	key := "key1"
	expected := testValue{ID: "3", Name: "Charlie"}

	result, err := lc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		return expected, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)

	// Verify L1 was populated
	l1Value, found := lc.GetWithoutTouch(key)
	assert.True(t, found)
	assert.Equal(t, expected, l1Value)

	// Verify Redis was populated
	data, err := redisClient.Get(t.Context(), lc.RedisKey(key)).Bytes()
	require.NoError(t, err)

	var redisValue testValue
	err = json.Unmarshal(data, &redisValue)
	require.NoError(t, err)
	assert.Equal(t, expected, redisValue)
}

func TestLayeredCache_RedisErrorFallthrough(t *testing.T) {
	t.Parallel()
	// Use a client pointing to a non-existent Redis
	badClient := redis.NewClient(&redis.Options{
		Addr:        "localhost:1", // invalid port
		DialTimeout: 100 * time.Millisecond,
	})
	defer badClient.Close()

	lc := NewLayeredCache[testValue](LayeredConfig[testValue]{
		L1TTL:        5 * time.Second,
		RedisTTL:     30 * time.Second,
		RedisClient:  badClient,
		RedisPrefix:  "test:bad",
		RedisTimeout: 200 * time.Millisecond,
	})
	defer lc.Close(t.Context())

	expected := testValue{ID: "4", Name: "Diana"}

	result, err := lc.GetOrSet(t.Context(), "key1", func(_ context.Context, _ string) (testValue, error) {
		return expected, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestLayeredCache_Delete(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	key := "key1"
	value := testValue{ID: "5", Name: "Eve"}

	// Populate both tiers
	lc.Set(t.Context(), key, value)

	// Verify both populated
	_, found := lc.GetWithoutTouch(key)
	assert.True(t, found)
	_, err := redisClient.Get(t.Context(), lc.RedisKey(key)).Bytes()
	require.NoError(t, err)

	// Delete
	lc.Delete(t.Context(), key)

	// Verify both cleared
	_, found = lc.GetWithoutTouch(key)
	assert.False(t, found)
	_, err = redisClient.Get(t.Context(), lc.RedisKey(key)).Result()
	assert.ErrorIs(t, err, redis.Nil)
}

func TestLayeredCache_InvalidateL1(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	key := "key1"
	value := testValue{ID: "6", Name: "Frank"}

	// Populate both tiers
	lc.Set(t.Context(), key, value)

	// Invalidate L1 only
	lc.InvalidateL1(key)

	// L1 should be cleared
	_, found := lc.GetWithoutTouch(key)
	assert.False(t, found)

	// Redis should still have the value
	data, err := redisClient.Get(t.Context(), lc.RedisKey(key)).Bytes()
	require.NoError(t, err)

	var redisValue testValue
	err = json.Unmarshal(data, &redisValue)
	require.NoError(t, err)
	assert.Equal(t, value, redisValue)
}

func TestLayeredCache_SetWritesBoth(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	key := "key1"
	value := testValue{ID: "7", Name: "Grace"}

	lc.Set(t.Context(), key, value)

	// Verify L1
	l1Value, found := lc.GetWithoutTouch(key)
	assert.True(t, found)
	assert.Equal(t, value, l1Value)

	// Verify Redis
	data, err := redisClient.Get(t.Context(), lc.RedisKey(key)).Bytes()
	require.NoError(t, err)

	var redisValue testValue
	err = json.Unmarshal(data, &redisValue)
	require.NoError(t, err)
	assert.Equal(t, value, redisValue)
}

func TestLayeredCache_Singleflight(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	var callCount atomic.Int32
	expected := testValue{ID: "8", Name: "Hank"}
	callback := func(_ context.Context, _ string) (testValue, error) { //nolint:unparam
		time.Sleep(100 * time.Millisecond)
		callCount.Add(1)

		return expected, nil
	}

	var wg errgroup.Group
	results := make([]testValue, 10)
	for i := range 10 {
		wg.Go(func() error {
			val, err := lc.GetOrSet(t.Context(), "key1", callback)
			if err != nil {
				return err
			}
			results[i] = val

			return nil
		})
	}

	err := wg.Wait()
	require.NoError(t, err)

	// All should return the same value
	for i, result := range results {
		assert.Equal(t, expected, result, "result %d mismatch", i)
	}

	// Callback should only be called once due to singleflight
	assert.Equal(t, int32(1), callCount.Load())
}

func TestLayeredCache_RedisRefresh_TriggeredWhenStale(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	redisTTL := 10 * time.Second
	refreshInterval := 100 * time.Millisecond

	lc := NewLayeredCache[testValue](LayeredConfig[testValue]{
		L1TTL:                500 * time.Millisecond,
		RedisTTL:             redisTTL,
		RedisRefreshInterval: refreshInterval,
		RedisRefreshTimeout:  5 * time.Second,
		RedisClient:          redisClient,
		RedisPrefix:          fmt.Sprintf("test:%s", t.Name()),
	})
	defer lc.Close(t.Context())

	key := "key1"
	staleValue := testValue{ID: "stale", Name: "StaleData"}
	freshValue := testValue{ID: "fresh", Name: "FreshData"}

	// Populate Redis with a value that has a short remaining TTL (simulating age)
	data, err := json.Marshal(staleValue)
	require.NoError(t, err)
	// Set with a TTL such that age = redisTTL - remainingTTL > refreshInterval
	remainingTTL := redisTTL - refreshInterval - 50*time.Millisecond
	err = redisClient.Set(t.Context(), lc.RedisKey(key), data, remainingTTL).Err()
	require.NoError(t, err)

	// Wait for L1 to expire so we hit Redis
	time.Sleep(600 * time.Millisecond)

	var callCount atomic.Int32
	result, err := lc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callCount.Add(1)

		return freshValue, nil
	})

	require.NoError(t, err)
	// Should return the stale value immediately
	assert.Equal(t, staleValue, result)

	// Wait for background refresh to complete
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		redisData, err := redisClient.Get(t.Context(), lc.RedisKey(key)).Bytes()
		if !assert.NoError(c, err) {
			return
		}
		var redisValue testValue
		err = json.Unmarshal(redisData, &redisValue)
		assert.NoError(c, err)
		assert.Equal(c, freshValue, redisValue)
	}, 2*time.Second, 50*time.Millisecond)

	assert.GreaterOrEqual(t, callCount.Load(), int32(1))
}

func TestLayeredCache_RedisRefresh_UpdatesBothRedisAndL1(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	redisTTL := 10 * time.Second
	refreshInterval := 100 * time.Millisecond

	lc := NewLayeredCache[testValue](LayeredConfig[testValue]{
		L1TTL:                500 * time.Millisecond,
		RedisTTL:             redisTTL,
		RedisRefreshInterval: refreshInterval,
		RedisRefreshTimeout:  5 * time.Second,
		RedisClient:          redisClient,
		RedisPrefix:          fmt.Sprintf("test:%s", t.Name()),
	})
	defer lc.Close(t.Context())

	key := "key1"
	staleValue := testValue{ID: "stale", Name: "StaleData"}
	freshValue := testValue{ID: "fresh", Name: "FreshData"}

	// Populate Redis with a stale entry
	data, err := json.Marshal(staleValue)
	require.NoError(t, err)
	remainingTTL := redisTTL - refreshInterval - 50*time.Millisecond
	err = redisClient.Set(t.Context(), lc.RedisKey(key), data, remainingTTL).Err()
	require.NoError(t, err)

	// Wait for L1 to expire
	time.Sleep(600 * time.Millisecond)

	_, err = lc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		return freshValue, nil
	})
	require.NoError(t, err)

	// Wait for background refresh
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		// Check Redis
		redisData, err := redisClient.Get(t.Context(), lc.RedisKey(key)).Bytes()
		if !assert.NoError(c, err) {
			return
		}
		var redisValue testValue
		err = json.Unmarshal(redisData, &redisValue)
		assert.NoError(c, err)
		assert.Equal(c, freshValue, redisValue)

		// Check L1
		l1Value, found := lc.GetWithoutTouch(key)
		assert.True(c, found)
		assert.Equal(c, freshValue, l1Value)
	}, 2*time.Second, 50*time.Millisecond)
}

func TestLayeredCache_RedisRefresh_ErrorKeepsStaleValue(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	redisTTL := 10 * time.Second
	refreshInterval := 100 * time.Millisecond

	lc := NewLayeredCache[testValue](LayeredConfig[testValue]{
		L1TTL:                500 * time.Millisecond,
		RedisTTL:             redisTTL,
		RedisRefreshInterval: refreshInterval,
		RedisRefreshTimeout:  5 * time.Second,
		RedisClient:          redisClient,
		RedisPrefix:          fmt.Sprintf("test:%s", t.Name()),
	})
	defer lc.Close(t.Context())

	key := "key1"
	staleValue := testValue{ID: "stale", Name: "StaleData"}

	// Populate Redis with a stale entry
	data, err := json.Marshal(staleValue)
	require.NoError(t, err)
	remainingTTL := redisTTL - refreshInterval - 50*time.Millisecond
	err = redisClient.Set(t.Context(), lc.RedisKey(key), data, remainingTTL).Err()
	require.NoError(t, err)

	// Wait for L1 to expire
	time.Sleep(600 * time.Millisecond)

	var callCount atomic.Int32
	result, err := lc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callCount.Add(1)

		return testValue{}, fmt.Errorf("database unavailable")
	})

	require.NoError(t, err)
	assert.Equal(t, staleValue, result)

	// Wait for background refresh attempt to complete
	time.Sleep(200 * time.Millisecond)

	// Redis should still have the stale value (not deleted)
	redisData, err := redisClient.Get(t.Context(), lc.RedisKey(key)).Bytes()
	require.NoError(t, err)
	var redisValue testValue
	err = json.Unmarshal(redisData, &redisValue)
	require.NoError(t, err)
	assert.Equal(t, staleValue, redisValue)

	assert.GreaterOrEqual(t, callCount.Load(), int32(1))
}

func TestLayeredCache_RedisRefresh_Disabled(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	// No RedisRefreshInterval set (defaults to 0)
	lc := newTestLayeredCache(t, redisClient)
	defer lc.Close(t.Context())

	key := "key1"
	expected := testValue{ID: "no-refresh", Name: "NoRefresh"}

	// Store in Redis
	data, err := json.Marshal(expected)
	require.NoError(t, err)
	err = redisClient.Set(t.Context(), lc.RedisKey(key), data, 30*time.Second).Err()
	require.NoError(t, err)

	var callCount atomic.Int32
	result, err := lc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callCount.Add(1)

		return testValue{ID: "different", Name: "Different"}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)

	// Wait to make sure no background refresh happens
	time.Sleep(200 * time.Millisecond)

	// Callback should not have been called (Redis hit, no refresh)
	assert.Equal(t, int32(0), callCount.Load())
}

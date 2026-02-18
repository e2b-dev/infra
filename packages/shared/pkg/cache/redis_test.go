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

func newTestRedisCache(t *testing.T, redisClient redis.UniversalClient) *RedisCache[testValue] {
	t.Helper()

	return NewRedisCache[testValue](RedisConfig{
		TTL:         30 * time.Second,
		RedisClient: redisClient,
		RedisPrefix: fmt.Sprintf("test:%s", t.Name()),
	})
}

func TestRedisNoPTTL_MatchesRedisBehavior(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	// Set a key with no expiration
	err := redisClient.Set(t.Context(), "test:no-ttl", "value", 0).Err()
	require.NoError(t, err)

	pttl := redisClient.PTTL(t.Context(), "test:no-ttl").Val()
	assert.Equal(t, redisNoPTTL, pttl, "redisNoPTTL constant should match PTTL result for a key with no expiration")
}

func TestRedisCache_RedisHit(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	rc := newTestRedisCache(t, redisClient)
	defer rc.Close(t.Context())

	key := "key1"
	expected := testValue{ID: "2", Name: "Bob"}

	// Store directly in Redis
	data, err := json.Marshal(expected)
	require.NoError(t, err)
	err = redisClient.Set(t.Context(), rc.RedisKey(key), data, 30*time.Second).Err()
	require.NoError(t, err)

	callbackCalled := false
	result, err := rc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callbackCalled = true

		return testValue{}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.False(t, callbackCalled, "callback should not be called on Redis hit")
}

func TestRedisCache_CallbackFallback(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	rc := newTestRedisCache(t, redisClient)
	defer rc.Close(t.Context())

	key := "key1"
	expected := testValue{ID: "3", Name: "Charlie"}

	result, err := rc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		return expected, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)

	// Verify Redis was populated
	data, err := redisClient.Get(t.Context(), rc.RedisKey(key)).Bytes()
	require.NoError(t, err)

	var redisValue testValue
	err = json.Unmarshal(data, &redisValue)
	require.NoError(t, err)
	assert.Equal(t, expected, redisValue)
}

func TestRedisCache_RedisErrorFallthrough(t *testing.T) {
	t.Parallel()
	// Use a client pointing to a non-existent Redis
	badClient := redis.NewClient(&redis.Options{
		Addr:        "localhost:1", // invalid port
		DialTimeout: 100 * time.Millisecond,
	})
	defer badClient.Close()

	rc := NewRedisCache[testValue](RedisConfig{
		TTL:          30 * time.Second,
		RedisClient:  badClient,
		RedisPrefix:  "test:bad",
		RedisTimeout: 200 * time.Millisecond,
	})
	defer rc.Close(t.Context())

	expected := testValue{ID: "4", Name: "Diana"}

	result, err := rc.GetOrSet(t.Context(), "key1", func(_ context.Context, _ string) (testValue, error) {
		return expected, nil
	})

	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestRedisCache_Delete(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	rc := newTestRedisCache(t, redisClient)
	defer rc.Close(t.Context())

	key := "key1"
	value := testValue{ID: "5", Name: "Eve"}

	// Populate Redis
	rc.Set(t.Context(), key, value)

	// Verify populated
	_, err := redisClient.Get(t.Context(), rc.RedisKey(key)).Bytes()
	require.NoError(t, err)

	// Delete
	rc.Delete(t.Context(), key)

	// Verify cleared
	_, err = redisClient.Get(t.Context(), rc.RedisKey(key)).Result()
	assert.ErrorIs(t, err, redis.Nil)
}

func TestRedisCache_SetWritesRedis(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	rc := newTestRedisCache(t, redisClient)
	defer rc.Close(t.Context())

	key := "key1"
	value := testValue{ID: "7", Name: "Grace"}

	rc.Set(t.Context(), key, value)

	// Verify Redis
	data, err := redisClient.Get(t.Context(), rc.RedisKey(key)).Bytes()
	require.NoError(t, err)

	var redisValue testValue
	err = json.Unmarshal(data, &redisValue)
	require.NoError(t, err)
	assert.Equal(t, value, redisValue)
}

func TestRedisCache_Singleflight(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)
	rc := newTestRedisCache(t, redisClient)
	defer rc.Close(t.Context())

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
			val, err := rc.GetOrSet(t.Context(), "key1", callback)
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

func TestRedisCache_RedisRefresh_TriggeredWhenStale(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	redisTTL := 10 * time.Second
	refreshInterval := 100 * time.Millisecond

	rc := NewRedisCache[testValue](RedisConfig{
		TTL:             redisTTL,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  5 * time.Second,
		RedisClient:     redisClient,
		RedisPrefix:     fmt.Sprintf("test:%s", t.Name()),
	})
	defer rc.Close(t.Context())

	key := "key1"
	staleValue := testValue{ID: "stale", Name: "StaleData"}
	freshValue := testValue{ID: "fresh", Name: "FreshData"}

	// Populate Redis with a value that has a short remaining TTL (simulating age)
	data, err := json.Marshal(staleValue)
	require.NoError(t, err)
	// Set with a TTL such that age = redisTTL - remainingTTL > refreshInterval
	remainingTTL := redisTTL - refreshInterval - 50*time.Millisecond
	err = redisClient.Set(t.Context(), rc.RedisKey(key), data, remainingTTL).Err()
	require.NoError(t, err)

	var callCount atomic.Int32
	result, err := rc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callCount.Add(1)

		return freshValue, nil
	})

	require.NoError(t, err)
	// Should return the stale value immediately
	assert.Equal(t, staleValue, result)

	// Wait for background refresh to complete
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		redisData, err := redisClient.Get(t.Context(), rc.RedisKey(key)).Bytes()
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

func TestRedisCache_RedisRefresh_UpdatesRedis(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	redisTTL := 10 * time.Second
	refreshInterval := 100 * time.Millisecond

	rc := NewRedisCache[testValue](RedisConfig{
		TTL:             redisTTL,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  5 * time.Second,
		RedisClient:     redisClient,
		RedisPrefix:     fmt.Sprintf("test:%s", t.Name()),
	})
	defer rc.Close(t.Context())

	key := "key1"
	staleValue := testValue{ID: "stale", Name: "StaleData"}
	freshValue := testValue{ID: "fresh", Name: "FreshData"}

	// Populate Redis with a stale entry
	data, err := json.Marshal(staleValue)
	require.NoError(t, err)
	remainingTTL := redisTTL - refreshInterval - 50*time.Millisecond
	err = redisClient.Set(t.Context(), rc.RedisKey(key), data, remainingTTL).Err()
	require.NoError(t, err)

	_, err = rc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		return freshValue, nil
	})
	require.NoError(t, err)

	// Wait for background refresh
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		redisData, err := redisClient.Get(t.Context(), rc.RedisKey(key)).Bytes()
		if !assert.NoError(c, err) {
			return
		}
		var redisValue testValue
		err = json.Unmarshal(redisData, &redisValue)
		assert.NoError(c, err)
		assert.Equal(c, freshValue, redisValue)
	}, 2*time.Second, 50*time.Millisecond)
}

func TestRedisCache_RedisRefresh_ErrorKeepsStaleValue(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	redisTTL := 10 * time.Second
	refreshInterval := 100 * time.Millisecond

	rc := NewRedisCache[testValue](RedisConfig{
		TTL:             redisTTL,
		RefreshInterval: refreshInterval,
		RefreshTimeout:  5 * time.Second,
		RedisClient:     redisClient,
		RedisPrefix:     fmt.Sprintf("test:%s", t.Name()),
	})
	defer rc.Close(t.Context())

	key := "key1"
	staleValue := testValue{ID: "stale", Name: "StaleData"}

	// Populate Redis with a stale entry
	data, err := json.Marshal(staleValue)
	require.NoError(t, err)
	remainingTTL := redisTTL - refreshInterval - 50*time.Millisecond
	err = redisClient.Set(t.Context(), rc.RedisKey(key), data, remainingTTL).Err()
	require.NoError(t, err)

	var callCount atomic.Int32
	result, err := rc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
		callCount.Add(1)

		return testValue{}, fmt.Errorf("database unavailable")
	})

	require.NoError(t, err)
	assert.Equal(t, staleValue, result)

	// Wait for background refresh attempt to complete
	time.Sleep(200 * time.Millisecond)

	// Redis should still have the stale value (not deleted)
	redisData, err := redisClient.Get(t.Context(), rc.RedisKey(key)).Bytes()
	require.NoError(t, err)
	var redisValue testValue
	err = json.Unmarshal(redisData, &redisValue)
	require.NoError(t, err)
	assert.Equal(t, staleValue, redisValue)

	assert.GreaterOrEqual(t, callCount.Load(), int32(1))
}

func TestRedisCache_RedisRefresh_Disabled(t *testing.T) {
	t.Parallel()
	redisClient := redis_utils.SetupInstance(t)

	// No RedisRefreshInterval set (defaults to 0)
	rc := newTestRedisCache(t, redisClient)
	defer rc.Close(t.Context())

	key := "key1"
	expected := testValue{ID: "no-refresh", Name: "NoRefresh"}

	// Store in Redis
	data, err := json.Marshal(expected)
	require.NoError(t, err)
	err = redisClient.Set(t.Context(), rc.RedisKey(key), data, 30*time.Second).Err()
	require.NoError(t, err)

	var callCount atomic.Int32
	result, err := rc.GetOrSet(t.Context(), key, func(_ context.Context, _ string) (testValue, error) {
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

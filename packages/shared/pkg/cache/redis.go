package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	RedisRefreshIntervalOff = 0

	// redisNoPTTL is the value returned by go-redis PTTL when a key has no expiration.
	// Redis returns the integer -1; go-redis converts it to -1 * time.Nanosecond.
	redisNoPTTL = -1 * time.Nanosecond
)

// RedisConfig holds the configuration for a RedisCache.
type RedisConfig struct {
	RedisClient redis.UniversalClient
	TTL         time.Duration
	// RefreshInterval triggers a background refresh of a Redis entry
	// when its age exceeds this duration. 0 = no background refresh.
	RefreshInterval time.Duration
	// RefreshTimeout is the timeout for the callback during Redis refresh.
	// Defaults to 30s.
	RefreshTimeout time.Duration
	RedisTimeout   time.Duration // default 2s
	RedisPrefix    string        // e.g. "template:build"
}

// RedisCache is a generic two-tier cache: Redis + user callback.
type RedisCache[V any] struct {
	config       RedisConfig
	fetchGroup   singleflight.Group
	redisRefresh singleflight.Group
}

// NewRedisCache creates a new RedisCache with the given configuration.
func NewRedisCache[V any](config RedisConfig) *RedisCache[V] {
	if config.RedisTimeout == 0 {
		config.RedisTimeout = 2 * time.Second
	}

	if config.RefreshTimeout == 0 {
		config.RefreshTimeout = 30 * time.Second
	}

	return &RedisCache[V]{
		config: config,
	}
}

// GetOrSet retrieves a value from Redis, falling back to dataCallback on miss.
// On callback hit, the value is backfilled into Redis.
// Singleflight deduplicates concurrent misses for the same key.
func (rc *RedisCache[V]) GetOrSet(ctx context.Context, key string, dataCallback DataCallback[V]) (V, error) {
	// Fast path: try Redis outside singleflight
	value, remainingTTL, err := rc.getFromRedis(ctx, key)
	if err == nil {
		// Check if eligible for refresh and refresh if necessary
		rc.handleRefresh(ctx, key, remainingTTL, dataCallback)

		return value, nil
	}

	// Cache miss: deduplicate concurrent fetches
	type result struct {
		value V
		err   error
	}

	r, _, _ := rc.fetchGroup.Do(key, func() (any, error) {
		ctx := context.WithoutCancel(ctx)
		// Double-check Redis (another goroutine may have populated it)
		if v, _, redisErr := rc.getFromRedis(ctx, key); redisErr == nil {
			return result{value: v}, nil
		}

		// Call the data callback with rc.config.RefreshTimeout timeout
		callbackCtx, cancel := context.WithTimeout(ctx, rc.config.RefreshTimeout)
		defer cancel()
		v, cbErr := dataCallback(callbackCtx, key)
		if cbErr != nil {
			return result{err: cbErr}, nil
		}

		// Backfill into Redis
		rc.setInRedis(ctx, key, v)

		return result{value: v}, nil
	})

	res := r.(result)

	return res.value, res.err
}

// Set stores a value in Redis.
func (rc *RedisCache[V]) Set(ctx context.Context, key string, value V) {
	rc.setInRedis(ctx, key, value)
}

// Delete removes a value from Redis.
func (rc *RedisCache[V]) Delete(ctx context.Context, key string) {
	rc.deleteFromRedis(ctx, key)
}

// Close is a no-op (no background goroutines to stop).
func (rc *RedisCache[V]) Close(_ context.Context) error {
	return nil
}

// RedisClient returns the underlying Redis client for custom operations (e.g. Lua scripts).
func (rc *RedisCache[V]) RedisClient() redis.UniversalClient {
	return rc.config.RedisClient
}

// RedisKey returns the full Redis key for a given cache key.
func (rc *RedisCache[V]) RedisKey(key string) string {
	return fmt.Sprintf("%s:%s", rc.config.RedisPrefix, key)
}

// getFromRedis fetches both the value and remaining TTL from Redis using a pipeline.
func (rc *RedisCache[V]) getFromRedis(ctx context.Context, key string) (V, time.Duration, error) {
	var zero V

	ctx, cancel := context.WithTimeout(ctx, rc.config.RedisTimeout)
	defer cancel()

	redisKey := rc.RedisKey(key)

	pipe := rc.config.RedisClient.Pipeline()
	getCmd := pipe.Get(ctx, redisKey)
	ttlCmd := pipe.PTTL(ctx, redisKey)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return zero, 0, err
	}

	data, err := getCmd.Bytes()
	if err != nil {
		return zero, 0, err
	}

	var value V
	if err := json.Unmarshal(data, &value); err != nil {
		return zero, 0, fmt.Errorf("redis cache: failed to unmarshal value: %w", err)
	}

	remainingTTL := ttlCmd.Val()

	return value, remainingTTL, nil
}

// refreshRedis refreshes a Redis entry in the background by calling the data callback.
func (rc *RedisCache[V]) refreshRedis(ctx context.Context, key string, dataCallback DataCallback[V]) {
	rc.redisRefresh.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, rc.config.RefreshTimeout)
		defer cancel()

		value, err := dataCallback(ctx, key)
		if err != nil {
			logger.L().Warn(ctx, "RedisCache: Redis background refresh failed",
				zap.String("key", key),
				zap.Error(err))

			return nil, nil
		}

		rc.setInRedis(ctx, key, value)

		return nil, nil
	})
}

func (rc *RedisCache[V]) setInRedis(ctx context.Context, key string, value V) {
	ctx, cancel := context.WithTimeout(ctx, rc.config.RedisTimeout)
	defer cancel()

	data, err := json.Marshal(value)
	if err != nil {
		logger.L().Warn(ctx, "RedisCache: failed to marshal value for Redis",
			zap.String("key", key),
			zap.Error(err))

		return
	}

	redisKey := rc.RedisKey(key)
	if err := rc.config.RedisClient.Set(ctx, redisKey, data, rc.config.TTL).Err(); err != nil {
		logger.L().Warn(ctx, "RedisCache: Redis SET error",
			zap.String("key", redisKey),
			zap.Error(err))
	}
}

func (rc *RedisCache[V]) deleteFromRedis(ctx context.Context, key string) {
	ctx, cancel := context.WithTimeout(ctx, rc.config.RedisTimeout)
	defer cancel()

	redisKey := rc.RedisKey(key)
	if err := rc.config.RedisClient.Del(ctx, redisKey).Err(); err != nil {
		logger.L().Warn(ctx, "RedisCache: Redis DEL error",
			zap.String("key", redisKey),
			zap.Error(err))
	}
}

// handleRefresh checks if the key should be refreshed in the background and refreshes it if necessary
func (rc *RedisCache[V]) handleRefresh(ctx context.Context, key string, remainingTTL time.Duration, dataCallback DataCallback[V]) {
	// check if key isn't persistent, this shouldn't really happen as it should be set with an expiration time
	if remainingTTL == redisNoPTTL {
		logger.L().Warn(ctx, "redis key unexpectedly persistent", zap.String("key", key))

		return
	}

	// Check if key is ready for to be refreshed in the background
	age := rc.config.TTL - remainingTTL
	if rc.config.RefreshInterval != RedisRefreshIntervalOff && age > rc.config.RefreshInterval {
		go rc.refreshRedis(context.WithoutCancel(ctx), key, dataCallback)
	}
}

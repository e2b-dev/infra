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
	RedisRefreshIntervalOff = -1
)

// LayeredConfig holds the configuration for a LayeredCache.
type LayeredConfig[V any] struct {
	// L1 (in-memory) settings
	L1TTL             time.Duration
	L1RefreshInterval time.Duration
	L1CallbackTimeout time.Duration
	L1RefreshTimeout  time.Duration

	// L2 (Redis) settings
	RedisClient redis.UniversalClient
	RedisTTL    time.Duration
	// RedisRefreshInterval triggers a background refresh of a Redis entry
	// when its age exceeds this duration. 0 = no background refresh.
	RedisRefreshInterval time.Duration
	// RedisRefreshTimeout is the timeout for the L3 callback during Redis refresh.
	// Defaults to 30s.
	RedisRefreshTimeout time.Duration
	RedisTimeout        time.Duration // default 2s
	RedisPrefix         string        // e.g. "template:build"

	// Optional, passed through to L1
	ExtractKeyFunc ExtractKeyFunc[V]
}

// LayeredCache is a generic three-tier cache: L1 (in-memory) + L2 (Redis) + L3 (user callback).
type LayeredCache[V any] struct {
	l1           *Cache[V]
	config       LayeredConfig[V]
	redisRefresh singleflight.Group
}

// NewLayeredCache creates a new LayeredCache with the given configuration.
func NewLayeredCache[V any](config LayeredConfig[V]) *LayeredCache[V] {
	if config.RedisTimeout == 0 {
		config.RedisTimeout = 2 * time.Second
	}

	if config.RedisRefreshTimeout == 0 {
		config.RedisRefreshTimeout = 30 * time.Second
	}

	l1 := NewCache[V](Config[V]{
		TTL:             config.L1TTL,
		RefreshInterval: config.L1RefreshInterval,
		CallbackTimeout: config.L1CallbackTimeout,
		RefreshTimeout:  config.L1RefreshTimeout,
		ExtractKeyFunc:  config.ExtractKeyFunc,
	})

	return &LayeredCache[V]{
		l1:     l1,
		config: config,
	}
}

// GetOrSet retrieves a value from L1, falling back to L2 (Redis) then L3 (dataCallback).
// On L3 hit, the value is backfilled into Redis.
// Singleflight from L1 automatically deduplicates the entire L2+L3 chain.
func (lc *LayeredCache[V]) GetOrSet(ctx context.Context, key string, dataCallback DataCallback[V]) (V, error) {
	return lc.l1.GetOrSet(ctx, key, lc.wrapCallback(dataCallback))
}

// Set stores a value in both L1 and Redis.
func (lc *LayeredCache[V]) Set(ctx context.Context, key string, value V) {
	lc.l1.Set(key, value)
	lc.setInRedis(ctx, key, value)
}

// Delete removes a value from both L1 and Redis.
func (lc *LayeredCache[V]) Delete(ctx context.Context, key string) {
	lc.l1.Delete(key)
	lc.deleteFromRedis(ctx, key)
}

// InvalidateL1 removes a key from L1 only.
// Use this when Redis was updated externally (e.g. via a Lua script).
func (lc *LayeredCache[V]) InvalidateL1(key string) {
	lc.l1.Delete(key)
}

// GetWithoutTouch retrieves a value from L1 without resetting its TTL.
func (lc *LayeredCache[V]) GetWithoutTouch(key string) (V, bool) {
	return lc.l1.GetWithoutTouch(key)
}

// Keys returns all keys currently in L1.
func (lc *LayeredCache[V]) Keys() []string {
	return lc.l1.Keys()
}

// Close stops the L1 cache.
func (lc *LayeredCache[V]) Close(ctx context.Context) error {
	return lc.l1.Close(ctx)
}

// RedisClient returns the underlying Redis client for custom operations (e.g. Lua scripts).
func (lc *LayeredCache[V]) RedisClient() redis.UniversalClient {
	return lc.config.RedisClient
}

// RedisKey returns the full Redis key for a given cache key.
func (lc *LayeredCache[V]) RedisKey(key string) string {
	return fmt.Sprintf("%s:%s", lc.config.RedisPrefix, key)
}

// wrapCallback wraps the user's L3 callback with L2 (Redis) lookup and backfill.
func (lc *LayeredCache[V]) wrapCallback(dataCallback DataCallback[V]) DataCallback[V] {
	return func(ctx context.Context, key string) (V, error) {
		// Try L2 (Redis)
		value, remainingTTL, err := lc.getFromRedis(ctx, key)
		if err == nil {
			age := lc.config.RedisTTL - remainingTTL
			if lc.config.RedisRefreshInterval != RedisRefreshIntervalOff && age > lc.config.RedisRefreshInterval {
				go lc.refreshRedis(context.WithoutCancel(ctx), key, dataCallback)
			}

			return value, nil
		}

		// Fall through to L3 (user callback)
		value, err = dataCallback(ctx, key)
		if err != nil {
			return value, err
		}

		// Backfill into Redis
		lc.setInRedis(ctx, key, value)

		return value, nil
	}
}

// getFromRedis fetches both the value and remaining TTL from Redis using a pipeline.
func (lc *LayeredCache[V]) getFromRedis(ctx context.Context, key string) (V, time.Duration, error) {
	var zero V

	ctx, cancel := context.WithTimeout(ctx, lc.config.RedisTimeout)
	defer cancel()

	redisKey := lc.RedisKey(key)

	pipe := lc.config.RedisClient.Pipeline()
	getCmd := pipe.Get(ctx, redisKey)
	ttlCmd := pipe.TTL(ctx, redisKey)

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
		return zero, 0, fmt.Errorf("layered cache: failed to unmarshal value: %w", err)
	}

	remainingTTL := ttlCmd.Val()

	return value, remainingTTL, nil
}

// refreshRedis refreshes a Redis entry in the background by calling the L3 callback.
func (lc *LayeredCache[V]) refreshRedis(ctx context.Context, key string, dataCallback DataCallback[V]) {
	lc.redisRefresh.Do(key, func() (any, error) { //nolint:unparam // singleflight API
		ctx, cancel := context.WithTimeout(ctx, lc.config.RedisRefreshTimeout)
		defer cancel()

		value, err := dataCallback(ctx, key)
		if err != nil {
			logger.L().Warn(ctx, "LayeredCache: Redis background refresh failed",
				zap.String("key", key),
				zap.Error(err))

			return nil, nil
		}

		lc.setInRedis(ctx, key, value)
		lc.l1.Set(key, value)

		return nil, nil
	})
}

func (lc *LayeredCache[V]) setInRedis(ctx context.Context, key string, value V) {
	ctx, cancel := context.WithTimeout(ctx, lc.config.RedisTimeout)
	defer cancel()

	data, err := json.Marshal(value)
	if err != nil {
		logger.L().Warn(ctx, "LayeredCache: failed to marshal value for Redis",
			zap.String("key", key),
			zap.Error(err))

		return
	}

	redisKey := lc.RedisKey(key)
	if err := lc.config.RedisClient.Set(ctx, redisKey, data, lc.config.RedisTTL).Err(); err != nil {
		logger.L().Warn(ctx, "LayeredCache: Redis SET error",
			zap.String("key", redisKey),
			zap.Error(err))
	}
}

func (lc *LayeredCache[V]) deleteFromRedis(ctx context.Context, key string) {
	ctx, cancel := context.WithTimeout(ctx, lc.config.RedisTimeout)
	defer cancel()

	redisKey := lc.RedisKey(key)
	if err := lc.config.RedisClient.Del(ctx, redisKey).Err(); err != nil {
		logger.L().Warn(ctx, "LayeredCache: Redis DEL error",
			zap.String("key", redisKey),
			zap.Error(err))
	}
}

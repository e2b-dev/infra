package redis_utils

import (
	"context"
	"fmt"
	"time"

	"github.com/bsm/redislock"
)

const lockKeyPrefix = "lock:"

func GetLockKey(key string) string {
	return fmt.Sprintf("%s%s", lockKeyPrefix, key)
}

// Lock abstracts a held distributed lock.
type Lock interface {
	Release(ctx context.Context) error
}

// Locker abstracts distributed lock acquisition.
type Locker interface {
	Obtain(ctx context.Context, key string, ttl time.Duration, opts *redislock.Options) (Lock, error)
}

// RedisLocker wraps redislock.Client.
type RedisLocker struct {
	Client *redislock.Client
}

func (l *RedisLocker) Obtain(ctx context.Context, key string, ttl time.Duration, opts *redislock.Options) (Lock, error) {
	return l.Client.Obtain(ctx, key, ttl, opts)
}

// NoopLocker always succeeds without acquiring any lock.
type NoopLocker struct{}

func (NoopLocker) Obtain(context.Context, string, time.Duration, *redislock.Options) (Lock, error) {
	return NoopLock{}, nil
}

// NoopLock is a lock that does nothing on release.
type NoopLock struct{}

func (NoopLock) Release(context.Context) error { return nil }

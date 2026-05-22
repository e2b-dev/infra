package redis

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
)

type storageLocker struct {
	redisClient redis.UniversalClient
	client      *redislock.Client
	option      *redislock.Options
	subManager  *subscriptionManager
	notifier    notifier
}

func newStorageLocker(redisClient redis.UniversalClient, subManager *subscriptionManager, n notifier) *storageLocker {
	return &storageLocker{
		redisClient: redisClient,
		client:      redislock.New(redisClient),
		option: &redislock.Options{
			RetryStrategy: redislock.NoRetry(),
		},
		subManager: subManager,
		notifier:   n,
	}
}

type storageLock struct {
	*redislock.Lock

	notifier notifier
}

func (l *storageLock) Release(ctx context.Context) error {
	if err := l.Lock.Release(ctx); err != nil {
		return err
	}

	// Hand off to the shared publisher
	l.notifier.Publish(ctx, getLockRoutingKey(l.Key()))

	return nil
}

func (l *storageLocker) Obtain(ctx context.Context, lockKey string, timeout time.Duration) (*storageLock, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lock, err := l.tryLock(ctx, lockKey, timeout)
	if err == nil {
		return lock, nil
	}
	if !errors.Is(err, redislock.ErrNotObtained) {
		return nil, err
	}

	ch, cleanup := l.subManager.subscribe(getLockRoutingKey(lockKey))
	defer cleanup()

	backoff := lockRetryMinInterval
	for {
		lock, err = l.tryLock(ctx, lockKey, timeout)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, redislock.ErrNotObtained) {
			return nil, err
		}

		timer := time.NewTimer(jitterBackoff(backoff))
		select {
		case <-ctx.Done():
			timer.Stop()

			return nil, ctx.Err()
		case <-ch:
			timer.Stop()
		case <-timer.C:
			backoff = min(backoff*2, lockRetryMaxInterval)
		}
	}
}

func (l *storageLocker) tryLock(ctx context.Context, lockKey string, timeout time.Duration) (*storageLock, error) {
	lock, err := l.client.Obtain(ctx, lockKey, timeout, l.option)
	if err != nil {
		return nil, err
	}

	return &storageLock{Lock: lock, notifier: l.notifier}, nil
}

func jitterBackoff(backoff time.Duration) time.Duration {
	factor := 1 + lockRetryJitter*(2*rand.Float64()-1)

	return time.Duration(float64(backoff) * factor)
}

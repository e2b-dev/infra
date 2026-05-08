package redis

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const lockNotifyTimeout = 5 * time.Second

type storageLocker struct {
	redisClient redis.UniversalClient
	client      *redislock.Client
	option      *redislock.Options
	subManager  *subscriptionManager
}

func newStorageLocker(redisClient redis.UniversalClient, subManager *subscriptionManager) *storageLocker {
	return &storageLocker{
		redisClient: redisClient,
		client:      redislock.New(redisClient),
		option: &redislock.Options{
			RetryStrategy: redislock.NoRetry(),
		},
		subManager: subManager,
	}
}

type storageLock struct {
	*redislock.Lock
	redisClient redis.UniversalClient
}

func (l *storageLock) Release(ctx context.Context) error {
	if err := l.Lock.Release(ctx); err != nil {
		return err
	}

	routingKey := getLockRoutingKey(l.Key())
	go l.publishReleaseNotification(context.WithoutCancel(ctx), routingKey)

	return nil
}

func (l *storageLocker) Obtain(ctx context.Context, lockKey string, timeout time.Duration) (*storageLock, error) {
	lockCtx, cancel := lockAcquireContext(ctx, timeout)
	if cancel != nil {
		defer cancel()
	}

	lock, err := l.tryLock(lockCtx, lockKey, timeout)
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
		lock, err = l.tryLock(lockCtx, lockKey, timeout)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, redislock.ErrNotObtained) {
			return nil, err
		}

		timer := time.NewTimer(jitterBackoff(backoff))
		select {
		case <-lockCtx.Done():
			timer.Stop()

			return nil, lockCtx.Err()
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

	return &storageLock{Lock: lock, redisClient: l.redisClient}, nil
}

func (l *storageLock) publishReleaseNotification(ctx context.Context, routingKey string) {
	ctx, cancel := context.WithTimeout(ctx, lockNotifyTimeout)
	defer cancel()

	if err := l.redisClient.Publish(ctx, globalStorageNotifyChannel, routingKey).Err(); err != nil {
		logger.L().Warn(ctx, "Failed to publish lock release notification", zap.Error(err))
	}
}

func lockAcquireContext(ctx context.Context, ttl time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, nil
	}

	return context.WithDeadline(ctx, time.Now().Add(ttl))
}

func jitterBackoff(backoff time.Duration) time.Duration {
	factor := 1 + lockRetryJitter*(2*rand.Float64()-1)

	return time.Duration(float64(backoff) * factor)
}

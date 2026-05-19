package redis

import (
	"context"
	"testing"
	"time"

	"github.com/bsm/redislock"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	testLockTimeout  = 30 * time.Minute
	testWaitTimeout  = time.Second
	testPollInterval = 10 * time.Millisecond
)

func TestStorageLocker_ObtainAfterReleaseNotification(t *testing.T) {
	t.Parallel()

	locker, subManager := setupTestLocker(t, true)

	key := getSandboxKey(uuid.NewString(), "lock-notification")
	lockKey := redis_utils.GetLockKey(key)
	routingKey := getLockRoutingKey(lockKey)

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)

	waiterDone := make(chan error, 1)
	go func() {
		waiterLock, obtainErr := locker.Obtain(t.Context(), lockKey, testLockTimeout)
		if obtainErr != nil {
			waiterDone <- obtainErr

			return
		}

		waiterDone <- waiterLock.Release(context.WithoutCancel(t.Context()))
	}()

	waitForLockWaiter(t, subManager, routingKey, waiterDone)

	require.NoError(t, lock.Release(context.WithoutCancel(t.Context())))
	requireNoErrorFromChannel(t, waiterDone)
}

func TestStorageLocker_ObtainTimesOutWhenHeld(t *testing.T) {
	t.Parallel()

	locker, _ := setupTestLocker(t, true)
	lockKey := redis_utils.GetLockKey(getSandboxKey(uuid.NewString(), "lock-timeout"))

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = lock.Release(context.WithoutCancel(t.Context()))
	})

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()

	_, err = locker.Obtain(ctx, lockKey, testLockTimeout)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStorageLocker_ObtainUsesProvidedContext(t *testing.T) {
	t.Parallel()

	locker, subManager := setupTestLocker(t, true)
	lockKey := redis_utils.GetLockKey(getSandboxKey(uuid.NewString(), "lock-parent-deadline"))
	routingKey := getLockRoutingKey(lockKey)

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)

	waiterDone := make(chan error, 1)
	go func() {
		waiterLock, obtainErr := locker.Obtain(t.Context(), lockKey, 25*time.Millisecond)
		if obtainErr != nil {
			waiterDone <- obtainErr

			return
		}

		waiterDone <- waiterLock.Release(context.WithoutCancel(t.Context()))
	}()

	waitForLockWaiter(t, subManager, routingKey, waiterDone)

	require.NoError(t, lock.Release(context.WithoutCancel(t.Context())))
	requireNoErrorFromChannel(t, waiterDone)
}

func TestStorageLocker_ObtainReturnsContextCancellation(t *testing.T) {
	t.Parallel()

	locker, _ := setupTestLocker(t, true)
	lockKey := redis_utils.GetLockKey(getSandboxKey(uuid.NewString(), "lock-context-cancel"))

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = lock.Release(context.WithoutCancel(t.Context()))
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = locker.Obtain(ctx, lockKey, testLockTimeout)
	require.ErrorIs(t, err, context.Canceled)
}

func TestStorageLocker_ObtainFallsBackWhenNotificationMissed(t *testing.T) {
	t.Parallel()

	// Do not start the subscription manager. This simulates a missed PubSub
	// notification and verifies the exponential fallback still makes progress.
	locker, subManager := setupTestLocker(t, false)

	lockKey := redis_utils.GetLockKey(getSandboxKey(uuid.NewString(), "lock-fallback"))
	routingKey := getLockRoutingKey(lockKey)

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)

	waiterDone := make(chan error, 1)
	go func() {
		waiterLock, obtainErr := locker.Obtain(t.Context(), lockKey, testLockTimeout)
		if obtainErr != nil {
			waiterDone <- obtainErr

			return
		}

		waiterDone <- waiterLock.Release(context.WithoutCancel(t.Context()))
	}()

	waitForLockWaiter(t, subManager, routingKey, waiterDone)
	require.NoError(t, lock.Lock.Release(context.WithoutCancel(t.Context())))
	requireNoErrorFromChannel(t, waiterDone)
}

func TestStorageLock_ReleaseUsesProvidedContext(t *testing.T) {
	t.Parallel()

	locker, _ := setupTestLocker(t, false)
	lockKey := redis_utils.GetLockKey(getSandboxKey(uuid.NewString(), "lock-canceled-release"))
	routingKey := getLockRoutingKey(lockKey)

	pubsub := locker.redisClient.Subscribe(t.Context(), globalStorageNotifyChannel)
	t.Cleanup(func() { require.NoError(t, pubsub.Close()) })
	_, err := pubsub.Receive(t.Context())
	require.NoError(t, err)

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)

	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, lock.Release(canceledCtx), context.Canceled)
	require.NoError(t, lock.Release(context.WithoutCancel(t.Context())))
	requirePubSubPayload(t, pubsub, routingKey)
}

func TestStorageLock_ReleaseReturnsErrorWhenLockAlreadyReleased(t *testing.T) {
	t.Parallel()

	locker, _ := setupTestLocker(t, true)
	lockKey := redis_utils.GetLockKey(getSandboxKey(uuid.NewString(), "lock-double-release"))

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)
	require.NoError(t, lock.Release(context.WithoutCancel(t.Context())))
	require.ErrorIs(t, lock.Release(context.WithoutCancel(t.Context())), redislock.ErrLockNotHeld)
}

func TestStorageLocker_IgnoresUnrelatedNotification(t *testing.T) {
	t.Parallel()

	locker, subManager := setupTestLocker(t, true)
	lockKey := redis_utils.GetLockKey(getSandboxKey(uuid.NewString(), "lock-unrelated-notification"))
	routingKey := getLockRoutingKey(lockKey)

	lock, err := locker.Obtain(t.Context(), lockKey, testLockTimeout)
	require.NoError(t, err)

	waiterDone := make(chan error, 1)
	go func() {
		waiterLock, obtainErr := locker.Obtain(t.Context(), lockKey, testLockTimeout)
		if obtainErr != nil {
			waiterDone <- obtainErr

			return
		}

		waiterDone <- waiterLock.Release(context.WithoutCancel(t.Context()))
	}()

	waitForLockWaiter(t, subManager, routingKey, waiterDone)
	subManager.dispatch(getLockRoutingKey(redis_utils.GetLockKey("unrelated")))

	var unexpectedErr error
	require.Never(t, func() bool {
		select {
		case unexpectedErr = <-waiterDone:
			return true
		default:
			return false
		}
	}, 5*testPollInterval, testPollInterval, "waiter completed after unrelated notification: %v", unexpectedErr)

	require.NoError(t, lock.Release(context.WithoutCancel(t.Context())))
	requireNoErrorFromChannel(t, waiterDone)
}

func TestJitterBackoffStaysWithinConfiguredRange(t *testing.T) {
	t.Parallel()

	base := 200 * time.Millisecond
	minBackoff := time.Duration(float64(base) * (1 - lockRetryJitter))
	maxBackoff := time.Duration(float64(base) * (1 + lockRetryJitter))

	for range 100 {
		backoff := jitterBackoff(base)
		require.GreaterOrEqual(t, backoff, minBackoff)
		require.LessOrEqual(t, backoff, maxBackoff)
	}
}

func setupTestLocker(t *testing.T, startSubManager bool) (*storageLocker, *subscriptionManager) {
	t.Helper()

	redisClient := redis_utils.SetupInstance(t)
	subManager := newSubscriptionManager(redisClient, globalStorageNotifyChannel)
	pub, err := newPublisher(redisClient, globalStorageNotifyChannel, noop.NewMeterProvider().Meter(meterScope))
	require.NoError(t, err)

	// The publisher always runs: lock release tests assert PubSub payloads
	// arrive, even when the in-process subscription manager is intentionally
	// disabled to exercise the timer fallback.
	go pub.run(t.Context())
	t.Cleanup(func() { pub.close(context.WithoutCancel(t.Context())) })

	if startSubManager {
		go subManager.start(t.Context())
		t.Cleanup(subManager.close)
	}

	return newStorageLocker(redisClient, subManager, pub), subManager
}

func waitForLockWaiter(t *testing.T, subManager *subscriptionManager, routingKey string, waiterDone <-chan error) {
	t.Helper()

	require.Eventually(t, func() bool {
		select {
		case err := <-waiterDone:
			require.FailNow(t, "lock waiter finished before registering", "err: %v", err)
		default:
		}

		subManager.mu.RLock()
		ready := len(subManager.waiters[routingKey]) > 0
		subManager.mu.RUnlock()

		return ready
	}, testWaitTimeout, testPollInterval, "lock waiter was not registered")
}

func requireNoErrorFromChannel(t *testing.T, ch <-chan error) {
	t.Helper()

	var err error
	require.Eventually(t, func() bool {
		select {
		case err = <-ch:
			return true
		default:
			return false
		}
	}, testWaitTimeout, testPollInterval, "operation did not complete")
	require.NoError(t, err)
}

func requirePubSubPayload(t *testing.T, pubsub *goredis.PubSub, expected string) {
	t.Helper()

	messages := pubsub.Channel()
	require.Eventually(t, func() bool {
		select {
		case msg := <-messages:
			return msg.Payload == expected
		default:
			return false
		}
	}, testWaitTimeout, testPollInterval, "expected PubSub payload was not received")
}

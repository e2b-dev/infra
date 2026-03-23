package redis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

func setupTestManager(t *testing.T) (*subscriptionManager, *Storage) {
	t.Helper()

	client := redis_utils.SetupInstance(t)
	storage := NewStorage(t.Context(), client)
	t.Cleanup(storage.Close)

	return storage.subManager, storage
}

func TestSubscriptionManager_SubscribeAndDispatch(t *testing.T) {
	t.Parallel()

	m, _ := setupTestManager(t)

	ch, cleanup := m.subscribe("key1")
	defer cleanup()

	m.dispatch("key1")

	select {
	case <-ch:
		// OK
	case <-time.After(time.Second):
		require.FailNow(t, "expected signal on channel")
	}
}

func TestSubscriptionManager_DispatchOnlyMatchingKey(t *testing.T) {
	t.Parallel()

	m, _ := setupTestManager(t)

	ch1, cleanup1 := m.subscribe("key1")
	defer cleanup1()
	ch2, cleanup2 := m.subscribe("key2")
	defer cleanup2()

	// Dispatch only to key2
	m.dispatch("key2")

	select {
	case <-ch2:
		// OK — key2 was signalled
	case <-time.After(time.Second):
		require.FailNow(t, "expected signal on ch2")
	}

	// ch1 should NOT have been signalled
	select {
	case <-ch1:
		require.FailNow(t, "ch1 should not have received a signal")
	case <-time.After(50 * time.Millisecond):
		// OK — no signal
	}
}

func TestSubscriptionManager_MultipleWaitersForSameKey(t *testing.T) {
	t.Parallel()

	m, _ := setupTestManager(t)

	const numWaiters = 5
	channels := make([]<-chan struct{}, numWaiters)
	cleanups := make([]func(), numWaiters)
	for i := range numWaiters {
		channels[i], cleanups[i] = m.subscribe("shared-key")
		defer cleanups[i]()
	}

	m.dispatch("shared-key")

	for i, ch := range channels {
		select {
		case <-ch:
			// OK
		case <-time.After(time.Second):
			require.FailNowf(t, "waiter did not receive signal", "waiter %d", i)
		}
	}
}

func TestSubscriptionManager_CleanupRemovesWaiter(t *testing.T) {
	t.Parallel()

	m, _ := setupTestManager(t)

	ch, cleanup := m.subscribe("key-cleanup")
	cleanup()

	// After cleanup, dispatch should not send to the removed channel
	m.dispatch("key-cleanup")

	select {
	case <-ch:
		require.FailNow(t, "should not receive signal after cleanup")
	case <-time.After(10 * time.Millisecond):
		// OK
	}

	// Verify the routing key entry was fully removed
	m.mu.RLock()
	_, exists := m.waiters["key-cleanup"]
	m.mu.RUnlock()
	assert.False(t, exists, "routing key should be removed from waiters map after last subscriber cleans up")
}

func TestSubscriptionManager_CleanupPartialRemoval(t *testing.T) {
	t.Parallel()

	m, _ := setupTestManager(t)

	ch1, cleanup1 := m.subscribe("key-partial")
	ch2, cleanup2 := m.subscribe("key-partial")
	defer cleanup2()

	// Remove only the first subscriber
	cleanup1()

	m.dispatch("key-partial")

	// ch1 should NOT receive (it was cleaned up)
	select {
	case <-ch1:
		require.FailNow(t, "ch1 should not receive after cleanup")
	case <-time.After(10 * time.Millisecond):
		// OK
	}

	// ch2 should still receive
	select {
	case <-ch2:
		// OK
	case <-time.After(time.Second):
		require.FailNow(t, "ch2 should still receive signal")
	}

	// The routing key should still exist (ch2 is still subscribed)
	m.mu.RLock()
	_, exists := m.waiters["key-partial"]
	m.mu.RUnlock()
	assert.True(t, exists, "routing key should still exist with remaining subscriber")
}

func TestSubscriptionManager_DoubleDispatchDoesNotBlock(t *testing.T) {
	t.Parallel()

	m, _ := setupTestManager(t)

	ch, cleanup := m.subscribe("key-double")
	defer cleanup()

	// Dispatch twice — the channel is buffered(1), so the second dispatch
	// should be silently dropped (not block).
	m.dispatch("key-double")
	m.dispatch("key-double")

	select {
	case <-ch:
		// OK — first signal consumed
	case <-time.After(time.Second):
		require.FailNow(t, "expected signal on channel")
	}

	// No second signal should be available
	select {
	case <-ch:
		require.FailNow(t, "should not have a second signal")
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestSubscriptionManager_ConcurrentSubscribeDispatchCleanup(t *testing.T) {
	t.Parallel()

	m, _ := setupTestManager(t)

	const goroutines = 20
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			ch, cleanup := m.subscribe("concurrent-key")
			defer cleanup()

			// Dispatch from every goroutine
			m.dispatch("concurrent-key")

			// Drain the channel (may or may not have a signal depending on timing)
			select {
			case <-ch:
			case <-time.After(100 * time.Millisecond):
			}
		}(i)
	}

	wg.Wait()

	// After all goroutines clean up, the waiters map should be empty for this key
	m.mu.RLock()
	_, exists := m.waiters["concurrent-key"]
	m.mu.RUnlock()
	assert.False(t, exists, "all waiters should be cleaned up")
}

func TestSubscriptionManager_PubSubEndToEnd(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	storage := NewStorage(t.Context(), client)
	t.Cleanup(storage.Close)

	routingKey := "test:routing:key"
	ch, cleanup := storage.subManager.subscribe(routingKey)
	defer cleanup()

	// Allow time for the PubSub subscription to be established
	time.Sleep(50 * time.Millisecond)

	// Publish via Redis (simulating what the callback does)
	err := client.Publish(t.Context(), getGlobalTransitionNotifyChannel(), routingKey).Err()
	require.NoError(t, err)

	select {
	case <-ch:
		// OK — received the PubSub notification
	case <-time.After(3 * time.Second):
		require.FailNow(t, "did not receive PubSub notification")
	}
}

func TestSubscriptionManager_PubSubIgnoresUnrelatedKeys(t *testing.T) {
	t.Parallel()

	client := redis_utils.SetupInstance(t)
	storage := NewStorage(t.Context(), client)
	t.Cleanup(storage.Close)

	ch, cleanup := storage.subManager.subscribe("my:sandbox:key")
	defer cleanup()

	time.Sleep(50 * time.Millisecond)

	// Publish a message with a different routing key
	err := client.Publish(t.Context(), getGlobalTransitionNotifyChannel(), "other:sandbox:key").Err()
	require.NoError(t, err)

	select {
	case <-ch:
		require.FailNow(t, "should not have received signal for a different routing key")
	case <-time.After(200 * time.Millisecond):
		// OK
	}
}

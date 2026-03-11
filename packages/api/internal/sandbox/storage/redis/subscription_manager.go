package redis

import (
	"context"
	"sync"

	"github.com/redis/go-redis/v9"
)

// subscriptionManager maintains a single Redis PubSub connection subscribed to
// globalTransitionNotifyChannel and fans out transition-complete signals to
// registered in-process waiters.
//
// One connection per API pod is sufficient regardless of how many sandboxes are
// concurrently transitioning. Each published message carries the per-sandbox
// routing key as its payload; the manager uses that to wake only the goroutines
// waiting on that specific sandbox.
type subscriptionManager struct {
	mu      sync.RWMutex
	waiters map[string]map[chan struct{}]struct{} // routingKey → registered waiters

	ps     *redis.PubSub
	cancel context.CancelFunc
}

func newSubscriptionManager(ctx context.Context, redisClient redis.UniversalClient) *subscriptionManager {
	ctx, cancel := context.WithCancel(ctx)

	m := &subscriptionManager{
		waiters: make(map[string]map[chan struct{}]struct{}),
		ps:      redisClient.Subscribe(ctx, getGlobalTransitionNotifyChannel()),
		cancel:  cancel,
	}

	go m.run(ctx)

	return m
}

// subscribe registers a waiter for the given routingKey (per-sandbox).
// The returned channel receives a signal when a transition-complete message
// arrives for that sandbox. The caller MUST invoke the returned cleanup function
// when done to avoid a memory leak.
func (m *subscriptionManager) subscribe(routingKey string) (<-chan struct{}, func()) {
	channel := make(chan struct{}, 1) // buffered so dispatch never blocks

	m.mu.Lock()
	if m.waiters[routingKey] == nil {
		m.waiters[routingKey] = make(map[chan struct{}]struct{})
	}
	m.waiters[routingKey][channel] = struct{}{}
	m.mu.Unlock()

	cleanup := func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		delete(m.waiters[routingKey], channel)
		if len(m.waiters[routingKey]) == 0 {
			delete(m.waiters, routingKey)
		}
	}

	return channel, cleanup
}

// run reads from the single global PubSub channel and dispatches signals to
// all waiters whose routing key matches the message payload.
// Runs for the lifetime of the subscriptionManager.
func (m *subscriptionManager) run(ctx context.Context) {
	ch := m.ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			m.dispatch(msg.Payload)
		}
	}
}

// dispatch signals all waiters registered for the given routing key.
func (m *subscriptionManager) dispatch(routingKey string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for waiter := range m.waiters[routingKey] {
		select {
		case waiter <- struct{}{}:
		default:
			// Waiter already has a pending signal; skip.
		}
	}
}

// close shuts down the subscription manager and its Redis PubSub connection.
func (m *subscriptionManager) close() {
	m.cancel()
	_ = m.ps.Close()
}

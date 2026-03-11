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
	mu      sync.Mutex
	waiters map[string][]chan struct{} // routingKey → registered waiters

	ps *redis.PubSub

	ctx    context.Context
	cancel context.CancelFunc
}

func newSubscriptionManager(redisClient redis.UniversalClient) *subscriptionManager {
	ctx, cancel := context.WithCancel(context.Background())

	m := &subscriptionManager{
		waiters: make(map[string][]chan struct{}),
		ps:      redisClient.Subscribe(ctx, getGlobalTransitionNotifyChannel()),
		ctx:     ctx,
		cancel:  cancel,
	}

	go m.run()

	return m
}

// subscribe registers a waiter for the given routingKey (per-sandbox).
// The returned channel receives a signal when a transition-complete message
// arrives for that sandbox. The caller MUST invoke the returned cleanup function
// when done to avoid a memory leak.
func (m *subscriptionManager) subscribe(routingKey string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1) // buffered so dispatch never blocks

	m.mu.Lock()
	m.waiters[routingKey] = append(m.waiters[routingKey], ch)
	m.mu.Unlock()

	cleanup := func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		waiters := m.waiters[routingKey]
		for i, w := range waiters {
			if w == ch {
				m.waiters[routingKey] = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}

		if len(m.waiters[routingKey]) == 0 {
			delete(m.waiters, routingKey)
		}
	}

	return ch, cleanup
}

// run reads from the single global PubSub channel and dispatches signals to
// all waiters whose routing key matches the message payload.
// Runs for the lifetime of the subscriptionManager.
func (m *subscriptionManager) run() {
	ch := m.ps.Channel()
	for {
		select {
		case <-m.ctx.Done():
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
	m.mu.Lock()
	waiters := m.waiters[routingKey]
	snapshot := make([]chan struct{}, len(waiters))
	copy(snapshot, waiters)
	m.mu.Unlock()

	for _, w := range snapshot {
		select {
		case w <- struct{}{}:
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

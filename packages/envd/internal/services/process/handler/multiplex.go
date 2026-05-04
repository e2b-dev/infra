package handler

import (
	"sync"
	"sync/atomic"
)

// MultiplexedChannel fans out values written to Source to every subscriber
// obtained via Fork. Each subscriber send is guarded by a done channel so
// a cancelled consumer can never wedge the fan-out loop.
type MultiplexedChannel[T any] struct {
	Source chan T

	mu       sync.RWMutex
	channels []*subscriber[T]
	exited   atomic.Bool
}

type subscriber[T any] struct {
	ch   chan T
	done chan struct{}
	once sync.Once
}

// cancel marks the subscriber as gone. Idempotent and non-blocking.
func (s *subscriber[T]) cancel() {
	s.once.Do(func() {
		close(s.done)
	})
}

// isCancelled reports whether cancel has been called.
func (s *subscriber[T]) isCancelled() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func NewMultiplexedChannel[T any](buffer int) *MultiplexedChannel[T] {
	c := &MultiplexedChannel[T]{
		Source: make(chan T, buffer),
	}

	go c.run()

	return c
}

// run is the fan-out loop. It delivers each Source value to every live
// subscriber and closes all consumer channels when Source is closed.
func (m *MultiplexedChannel[T]) run() {
	for v := range m.Source {
		m.mu.RLock()
		subs := m.channels
		m.mu.RUnlock()

		for _, s := range subs {
			// Skip already-cancelled subscribers.
			if s.isCancelled() {
				continue
			}

			select {
			case s.ch <- v:
			case <-s.done:
			}
		}
	}

	m.exited.Store(true)

	// Close all remaining consumer channels so `for range` loops exit.
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.channels {
		s.cancel()
		close(s.ch)
	}
	m.channels = nil
}

// Fork registers a new subscriber and returns its channel plus a cancel func.
// If Source is already closed it returns a pre-closed channel and a no-op cancel.
// The channel is bidirectional for backwards compat with start.go which writes
// a bootstrap event into it; new callers should treat it as receive-only.
func (m *MultiplexedChannel[T]) Fork() (chan T, func()) {
	if m.exited.Load() {
		ch := make(chan T)
		close(ch)

		return ch, func() {}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check under lock in case run() finished between the fast path and here.
	if m.exited.Load() {
		ch := make(chan T)
		close(ch)

		return ch, func() {}
	}

	s := &subscriber[T]{
		ch:   make(chan T),
		done: make(chan struct{}),
	}

	m.channels = append(m.channels, s)

	return s.ch, func() {
		m.remove(s)
	}
}

// remove unsubscribes s. Safe to call multiple times.
func (m *MultiplexedChannel[T]) remove(s *subscriber[T]) {
	// Cancel before locking so an in-flight fan-out send can unblock.
	s.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	for i, sub := range m.channels {
		if sub == s {
			m.channels = append(m.channels[:i], m.channels[i+1:]...)

			return
		}
	}
}

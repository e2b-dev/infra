package handler

import (
	"sync"
	"sync/atomic"
)

// MultiplexedChannel fans out values written to Source to every subscriber
// obtained via Fork. Each subscriber send is guarded by a done channel so
// a cancelled consumer can never wedge the fan-out loop.
//
// When no subscribers are connected, the fan-out loop blocks instead of
// draining Source.  This lets the Source buffer fill up and back-pressure
// the producer through the pipe.
type MultiplexedChannel[T any] struct {
	Source chan T

	mu       sync.RWMutex
	channels []*subscriber[T]
	exited   atomic.Bool
	done     chan struct{} // closed when run() returns

	subMu     sync.Mutex
	subSignal chan struct{} // closed+recreated on subscriber list change
	closed    atomic.Bool   // true after CloseSource
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
		Source:    make(chan T, buffer),
		done:      make(chan struct{}),
		subSignal: make(chan struct{}),
	}

	go c.run()

	return c
}

// run is the fan-out loop. It delivers each Source value to every live
// subscriber and closes all consumer channels when Source is closed.
//
// When no active subscribers remain after delivering a value, run
// stops consuming from Source until a subscriber appears.  This lets
// Source fill up, which back-pressures the reader goroutine, which
// in turn back-pressures the child process through the pipe.
func (m *MultiplexedChannel[T]) run() {
	for {
		// Wait for either a value from Source or a subscriber to appear.
		v, ok := m.receiveWhenReady()
		if !ok {
			break
		}

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

	for _, s := range m.channels {
		s.cancel()
		close(s.ch)
	}
	m.channels = nil

	m.mu.Unlock()
	close(m.done)
}

// receiveWhenReady reads the next value from Source, but only when at
// least one active subscriber exists.  When there are no subscribers
// it stops consuming so Source fills up and back-pressures the
// producer.  Returns (zero, false) when Source is closed.
//
// Callers that close Source must use CloseSource (not bare close)
// so the fan-out loop wakes up and observes the closed channel.
func (m *MultiplexedChannel[T]) receiveWhenReady() (v T, ok bool) {
	for {
		if m.closed.Load() {
			// Drain any remaining buffered values.
			v, ok = <-m.Source

			return v, ok
		}

		if m.HasSubscribers() {
			v, ok = <-m.Source

			return v, ok
		}

		// No active subscribers — wait for a change notification.
		// Re-check closed after grabbing the signal to avoid parking
		// on a signal that was created *by* CloseSource's notify.
		m.subMu.Lock()
		sig := m.subSignal
		m.subMu.Unlock()

		if m.closed.Load() {
			v, ok = <-m.Source

			return v, ok
		}

		<-sig
	}
}

// CloseSource closes the Source channel and wakes the fan-out loop.
func (m *MultiplexedChannel[T]) CloseSource() {
	m.closed.Store(true)
	close(m.Source)
	m.NotifySubscriberChange()
}

// NotifySubscriberChange wakes the fan-out if it's waiting.
func (m *MultiplexedChannel[T]) NotifySubscriberChange() {
	m.subMu.Lock()
	defer m.subMu.Unlock()

	close(m.subSignal)
	m.subSignal = make(chan struct{})
}

// HasSubscribers reports whether any non-cancelled subscriber exists.
func (m *MultiplexedChannel[T]) HasSubscribers() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.channels {
		if !s.isCancelled() {
			return true
		}
	}

	return false
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
	m.NotifySubscriberChange()

	return s.ch, func() {
		m.remove(s)
	}
}

// remove unsubscribes s. Safe to call multiple times.
func (m *MultiplexedChannel[T]) remove(s *subscriber[T]) {
	// Cancel before locking so an in-flight fan-out send can unblock.
	s.cancel()

	m.mu.Lock()

	for i, sub := range m.channels {
		if sub == s {
			m.channels = append(m.channels[:i], m.channels[i+1:]...)
			m.mu.Unlock()
			m.NotifySubscriberChange()

			return
		}
	}

	m.mu.Unlock()
}

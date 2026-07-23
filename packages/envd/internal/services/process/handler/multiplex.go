package handler

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// subscriberBuffer smooths bursts so a live subscriber that is briefly
	// behind is not treated as stalled.
	subscriberBuffer = 64

	// subscriberSendGrace bounds how long the fan-out loop waits for a
	// subscriber whose buffer is already full before disconnecting it.
	//
	// A subscriber that consumed nothing while a full buffer piled up and
	// then made no progress for this long is either gone without erroring
	// (e.g. its TCP connection was restored from a pause snapshot and now
	// leads nowhere, so writes to it block forever without failing) or is
	// pathologically slow. Without the bound, one such subscriber blocks
	// the fan-out loop and no other consumer of the process — including
	// freshly connected ones — receives output ever again.
	subscriberSendGrace = 5 * time.Second
)

// MultiplexedChannel fans out values written to Source to every subscriber
// obtained via Fork. Each subscriber send is guarded by a done channel so a
// cancelled consumer can never wedge the fan-out loop, and bounded by a
// grace timeout so a stalled consumer that never cancels (e.g. one whose
// connection died without erroring across a sandbox pause/resume) is
// disconnected instead of wedging the fan-out loop forever.
type MultiplexedChannel[T any] struct {
	Source chan T

	sendGrace time.Duration

	mu       sync.RWMutex
	channels []*subscriber[T]
	exited   atomic.Bool
}

type subscriber[T any] struct {
	ch     chan T
	done   chan struct{}
	kicked chan struct{}

	once     sync.Once
	kickOnce sync.Once
}

// cancel marks the subscriber as gone. Idempotent and non-blocking.
func (s *subscriber[T]) cancel() {
	s.once.Do(func() {
		close(s.done)
	})
}

// kick disconnects a subscriber that stopped draining its channel and
// signals the disconnection on the kicked channel. Idempotent and
// non-blocking.
func (s *subscriber[T]) kick() {
	s.kickOnce.Do(func() {
		close(s.kicked)
	})
	s.cancel()
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
	return newMultiplexedChannelWithGrace[T](buffer, subscriberSendGrace)
}

// newMultiplexedChannelWithGrace exists so tests can shorten the kick grace.
func newMultiplexedChannelWithGrace[T any](buffer int, sendGrace time.Duration) *MultiplexedChannel[T] {
	c := &MultiplexedChannel[T]{
		Source:    make(chan T, buffer),
		sendGrace: sendGrace,
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
			default:
				// The subscriber's buffer is full. Give it a bounded grace
				// to make progress, then disconnect it so it cannot block
				// the fan-out for every other consumer of this process.
				m.sendWithGrace(s, v)
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

// sendWithGrace waits up to sendGrace for the subscriber to accept v and
// kicks (disconnects) it when it does not. A slow-but-draining subscriber
// keeps its backpressure semantics: as long as it frees buffer space at
// least once per grace period it is never kicked.
func (m *MultiplexedChannel[T]) sendWithGrace(s *subscriber[T], v T) {
	timer := time.NewTimer(m.sendGrace)
	defer timer.Stop()

	select {
	case s.ch <- v:
	case <-s.done:
	case <-timer.C:
		s.kick()
	}
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

// Fork registers a new subscriber and returns its channel, a channel closed
// if the subscriber is forcibly disconnected for not consuming events (see
// subscriberSendGrace), and a cancel func.
// If Source is already closed it returns a pre-closed channel, a
// never-closed kicked channel, and a no-op cancel.
func (m *MultiplexedChannel[T]) Fork() (<-chan T, <-chan struct{}, func()) {
	if m.exited.Load() {
		return closedFork[T]()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check under lock in case run() finished between the fast path and here.
	if m.exited.Load() {
		return closedFork[T]()
	}

	s := &subscriber[T]{
		ch:     make(chan T, subscriberBuffer),
		done:   make(chan struct{}),
		kicked: make(chan struct{}),
	}

	m.channels = append(m.channels, s)

	return s.ch, s.kicked, func() {
		m.remove(s)
	}
}

func closedFork[T any]() (<-chan T, <-chan struct{}, func()) {
	ch := make(chan T)
	close(ch)

	return ch, make(chan struct{}), func() {}
}

// remove unsubscribes s. Safe to call multiple times.
func (m *MultiplexedChannel[T]) remove(s *subscriber[T]) {
	// Cancel before locking so an in-flight fan-out send can unblock.
	s.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	for i, sub := range m.channels {
		if sub == s {
			// New backing array so run()'s concurrent iteration is safe.
			m.channels = slices.Concat(m.channels[:i], m.channels[i+1:])

			return
		}
	}
}

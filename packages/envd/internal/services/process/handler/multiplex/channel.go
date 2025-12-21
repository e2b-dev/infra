package multiplex

import (
	"sync"
	"sync/atomic"
)

type Channel[T any] struct {
	Source              chan T
	consumers           []chan *Item[T]
	replayConsumerAdded chan struct{}
	mu                  sync.RWMutex
	exited              atomic.Bool
	queue               MemoryQueue[T]
}

// NewChannel creates a new Channel that multiplexes the source channel to multiple consumers.
// If replay is true, the Channel will attempt to replay the unconsumed items when a new consumer is added.
func NewChannel[T any](buffer int, replayable bool) *Channel[T] {
	m := &Channel[T]{
		Source:              make(chan T, buffer),
		replayConsumerAdded: make(chan struct{}),
	}

	go func() {
	serveLoop:
		for {
			select {
			case v, ok := <-m.Source:
				if !ok {
					break serveLoop
				}

				if !replayable {
					m.consume(v)

					continue
				}

				m.queue.Push(v)

				m.consumeSync()
			case <-m.replayConsumerAdded:
				if !replayable {
					continue
				}

				for m.consumeSync() {
					continue
				}
			}
		}

		m.exited.Store(true)

		for _, cons := range m.consumers {
			close(cons)
		}
	}()

	return m
}

func (m *Channel[T]) consumeSync() bool {
	v, ok := m.queue.Pop()
	if !ok {
		return false
	}

	var consumed bool

	for _, ack := range m.consume(v) {
		consumed = consumed || <-ack
	}

	if !consumed {
		m.queue.Unshift(v)
	}

	return consumed
}

func (m *Channel[T]) consume(v T) (acks []chan bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, cons := range m.consumers {
		item := newItem(v)
		cons <- item

		acks = append(acks, item.success)
	}

	return acks
}

// Fork forks a new channel that can be used to receive items from the multiplexer.
// If attemptReplay is true, the channel will attempt to replay the unconsumed items when a new consumer is added.
// The replay is only attempted if there is only one active fork from the channel and the channel must have been created with replayable set to true.
func (m *Channel[T]) Fork(attemptReplay bool) (chan *Item[T], func()) {
	if m.exited.Load() {
		ch := make(chan *Item[T])
		close(ch)

		return ch, func() {}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	consumer := make(chan *Item[T])

	m.consumers = append(m.consumers, consumer)

	if len(m.consumers) == 1 && attemptReplay {
		m.replayConsumerAdded <- struct{}{}
	}

	return consumer, func() {
		m.remove(consumer)
	}
}

func (m *Channel[T]) Send(v T) {
	m.Source <- v
}

func (m *Channel[T]) remove(consumer chan *Item[T]) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, ch := range m.consumers {
		if ch == consumer {
			m.consumers = append(m.consumers[:i], m.consumers[i+1:]...)

			return
		}
	}
}

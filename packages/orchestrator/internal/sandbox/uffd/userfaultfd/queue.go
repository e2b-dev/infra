package userfaultfd

import (
	"container/list"
	"iter"
	"sync"
)

// queue is a concurrency-safe heterogeneous FIFO queue.
// Any event type can be pushed into it, and drain extracts
// events of a specific type while leaving others in place.
type queue struct {
	mu sync.Mutex
	l  *list.List
}

func newQueue() *queue {
	return &queue{
		l: list.New(),
	}
}

// push adds an event to the back of the queue.
func (q *queue) push(item any) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.l.PushBack(item)
}

func (q *queue) prepend(q2 *queue) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q2.mu.Lock()
	defer q2.mu.Unlock()

	q.l.PushFrontList(q2.l)
}

func (q *queue) size() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.l.Len()
}

func (q *queue) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.l.Init()
}

// each returns an iterator over all items of type T in the queue.
// Items of other types are skipped.
func each[T any](q *queue) iter.Seq[T] {
	return func(yield func(T) bool) {
		q.mu.Lock()
		defer q.mu.Unlock()

		for e := q.l.Front(); e != nil; e = e.Next() {
			v, ok := e.Value.(T)
			if !ok {
				continue
			}

			if !yield(v) {
				break
			}
		}
	}
}

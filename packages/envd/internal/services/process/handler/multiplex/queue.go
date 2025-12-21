package multiplex

import (
	"sync"
)

type MemoryQueue[T any] struct {
	items []T
	mu    sync.RWMutex
}

func (q *MemoryQueue[T]) Push(v T) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.items = append(q.items, v)
}

func (q *MemoryQueue[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		var zero T

		return zero, false
	}

	item := q.items[0]
	q.items = q.items[1:]

	return item, true
}

func (q *MemoryQueue[T]) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return len(q.items)
}

func (q *MemoryQueue[T]) Unshift(v T) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.items = append([]T{v}, q.items...)
}

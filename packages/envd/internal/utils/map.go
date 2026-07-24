package utils

import "sync"

type Map[K comparable, V any] struct {
	m sync.Map
}

func NewMap[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{
		m: sync.Map{},
	}
}

func (m *Map[K, V]) Delete(key K) {
	m.m.Delete(key)
}

func (m *Map[K, V]) Load(key K) (value V, ok bool) {
	v, ok := m.m.Load(key)
	if !ok {
		return value, ok
	}

	return v.(V), ok
}

func (m *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	v, loaded := m.m.LoadAndDelete(key)
	if !loaded {
		return value, loaded
	}

	return v.(V), loaded
}

func (m *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	a, loaded := m.m.LoadOrStore(key, value)

	return a.(V), loaded
}

func (m *Map[K, V]) Range(f func(key K, value V) bool) {
	m.m.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}

func (m *Map[K, V]) Store(key K, value V) {
	m.m.Store(key, value)
}

// CompareAndDelete deletes the entry for key only if its value is still old,
// returning whether it deleted. Lets a caller evict its own entry without
// clobbering a newer value stored under the same key (e.g. a reused PID).
func (m *Map[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	return m.m.CompareAndDelete(key, old)
}

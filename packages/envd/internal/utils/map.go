package utils

import (
	"fmt"
	"sync"
)

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
		return
	}

	value, ok = v.(V)
	if !ok {
		panic(fmt.Sprintf("invalid value type: %T", v))
	}

	return
}

func (m *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	v, loaded := m.m.LoadAndDelete(key)
	if !loaded {
		return
	}

	value, cast := v.(V)
	if !cast {
		panic(fmt.Sprintf("invalid value type: %T", v))
	}

	return
}

func (m *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	a, loaded := m.m.LoadOrStore(key, value)
	if loaded {
		actual, loaded = a.(V)
	}
	return
}

func (m *Map[K, V]) Range(f func(key K, value V) bool) {
	m.m.Range(func(key, value any) bool {
		k, ok := key.(K)
		if !ok {
			panic(fmt.Sprintf("key: expected %T got %T", k, key))
		}
		v, ok := value.(V)
		if !ok {
			panic(fmt.Sprintf("value: expected %T got %T", v, value))
		}
		return f(k, v)
	})
}

func (m *Map[K, V]) Store(key K, value V) {
	m.m.Store(key, value)
}

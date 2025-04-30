package smap

import (
	cmap "github.com/orcaman/concurrent-map/v2"
)

type Map[V any] struct {
	m cmap.ConcurrentMap[string, V]
}

func New[V any]() *Map[V] {
	return &Map[V]{
		m: cmap.New[V](),
	}
}

func (m *Map[V]) Remove(key string) {
	m.m.Remove(key)
}

func (m *Map[V]) Get(key string) (V, bool) {
	return m.m.Get(key)
}

func (m *Map[V]) Insert(key string, value V) {
	m.m.Set(key, value)
}

func (m *Map[V]) Upsert(key string, value V, cb cmap.UpsertCb[V]) V {
	return m.m.Upsert(key, value, cb)
}

func (m *Map[V]) InsertIfAbsent(key string, value V) bool {
	return m.m.SetIfAbsent(key, value)
}

func (m *Map[V]) Items() map[string]V {
	return m.m.Items()
}

func (m *Map[V]) RemoveCb(key string, cb func(key string, v V, exists bool) bool) bool {
	return m.m.RemoveCb(key, cb)
}

func (m *Map[V]) Count() int {
	return m.m.Count()
}

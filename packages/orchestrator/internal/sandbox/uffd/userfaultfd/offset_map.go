package userfaultfd

import "sync/atomic"

// OffsetMap wraps a map that is non-thread-safe map for writes/reads, but make it thread safe to call the reset function.
// The TryAdd on the map is still non-thread-safe.
type OffsetMap struct {
	r atomic.Pointer[map[int64]struct{}]
}

func NewResetMap() *OffsetMap {
	m := &OffsetMap{
		r: atomic.Pointer[map[int64]struct{}]{},
	}

	m.r.Store(&map[int64]struct{}{})

	return m
}

func (r *OffsetMap) Reset() {
	r.r.Store(&map[int64]struct{}{})
}

func (r *OffsetMap) TryAdd(key int64) bool {
	m := *r.r.Load()

	if _, ok := m[key]; ok {
		return false
	}

	m[key] = struct{}{}

	return true
}

package userfaultfd

import "sync"

type OffsetMap struct {
	m  map[int64]struct{}
	mu sync.Mutex
}

func NewOffsetMap() *OffsetMap {
	return &OffsetMap{
		m: make(map[int64]struct{}),
	}
}

func (r *OffsetMap) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.m = make(map[int64]struct{})
}

func (r *OffsetMap) TryAdd(key int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.m[key]; ok {

		return false
	}

	r.m[key] = struct{}{}

	return true
}

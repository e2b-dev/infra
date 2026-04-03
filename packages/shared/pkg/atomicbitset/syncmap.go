package atomicbitset

import (
	"sync"

	"github.com/bits-and-blooms/bitset"
)

// SyncMap wraps a sync.Map to implement Bitset using block-index keys.
// This mirrors the original sync.Map-based dirty tracking for A/B testing.
type SyncMap struct {
	m sync.Map
	n uint
}

func NewSyncMap(n uint) *SyncMap {
	return &SyncMap{n: n}
}

func (s *SyncMap) Len() uint { return s.n }

func (s *SyncMap) Has(i uint) bool {
	if i >= s.n {
		return false
	}

	_, ok := s.m.Load(i)

	return ok
}

func (s *SyncMap) HasRange(lo, hi uint) bool {
	if lo >= hi {
		return true
	}
	if hi > s.n {
		hi = s.n
	}
	if lo >= hi {
		return false
	}

	for i := lo; i < hi; i++ {
		if _, ok := s.m.Load(i); !ok {
			return false
		}
	}

	return true
}

func (s *SyncMap) SetRange(lo, hi uint) {
	if hi > s.n {
		hi = s.n
	}

	for i := lo; i < hi; i++ {
		s.m.Store(i, struct{}{})
	}
}

func (s *SyncMap) BitSet() *bitset.BitSet {
	bs := bitset.New(s.n)
	s.m.Range(func(key, _ any) bool {
		bs.Set(key.(uint))

		return true
	})

	return bs
}

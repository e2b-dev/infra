package atomicbitset

import (
	"iter"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
)

// Roaring wraps a roaring bitmap with an internal RWMutex.
// Iterator is NOT locked — caller must prevent concurrent mutation.
type Roaring struct {
	mu sync.RWMutex
	bm *roaring.Bitmap
	n  uint
}

func NewRoaring(n uint) *Roaring {
	return &Roaring{
		bm: roaring.New(),
		n:  n,
	}
}

func (r *Roaring) Len() uint { return r.n }

func (r *Roaring) Has(i uint) bool {
	if i >= r.n {
		return false
	}

	r.mu.RLock()
	result := r.bm.ContainsInt(int(i))
	r.mu.RUnlock()

	return result
}

func (r *Roaring) HasRange(lo, hi uint) bool {
	if lo >= hi {
		return true
	}
	if hi > r.n {
		hi = r.n
	}
	if lo >= hi {
		return false
	}

	r.mu.RLock()
	result := r.bm.CardinalityInRange(uint64(lo), uint64(hi)) == uint64(hi-lo)
	r.mu.RUnlock()

	return result
}

func (r *Roaring) SetRange(lo, hi uint) {
	if hi > r.n {
		hi = r.n
	}
	if lo >= hi {
		return
	}

	r.mu.Lock()
	r.bm.AddRange(uint64(lo), uint64(hi))
	r.mu.Unlock()
}

func (r *Roaring) UnsafeIterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		it := r.bm.Iterator()
		for it.HasNext() {
			if !yield(uint(it.Next())) {
				return
			}
		}
	}
}

func (r *Roaring) Iterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		r.mu.RLock()
		defer r.mu.RUnlock()

		for v := range r.UnsafeIterator() {
			if !yield(v) {
				return
			}
		}
	}
}

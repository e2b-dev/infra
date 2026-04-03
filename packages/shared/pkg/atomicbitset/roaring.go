package atomicbitset

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/bits-and-blooms/bitset"
)

// Roaring wraps a roaring bitmap (32-bit) with an internal RWMutex.
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
	v := r.bm.Contains(uint32(i))
	r.mu.RUnlock()

	return v
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
	defer r.mu.RUnlock()

	for i := lo; i < hi; i++ {
		if !r.bm.Contains(uint32(i)) {
			return false
		}
	}

	return true
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

func (r *Roaring) BitSet() *bitset.BitSet {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.bm.ToBitSet()
}

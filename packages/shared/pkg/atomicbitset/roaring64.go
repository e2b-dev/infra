package atomicbitset

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2/roaring64"
	"github.com/bits-and-blooms/bitset"
)

// Roaring64 wraps a roaring64 bitmap (64-bit keys) with an internal RWMutex.
type Roaring64 struct {
	mu sync.RWMutex
	bm *roaring64.Bitmap
	n  uint
}

func NewRoaring64(n uint) *Roaring64 {
	return &Roaring64{
		bm: roaring64.New(),
		n:  n,
	}
}



func (r *Roaring64) Has(i uint) bool {
	if i >= r.n {
		return false
	}

	r.mu.RLock()
	v := r.bm.Contains(uint64(i))
	r.mu.RUnlock()

	return v
}

func (r *Roaring64) HasRange(lo, hi uint) bool {
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
		if !r.bm.Contains(uint64(i)) {
			return false
		}
	}

	return true
}

func (r *Roaring64) SetRange(lo, hi uint) {
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

func (r *Roaring64) BitSet() *bitset.BitSet {
	r.mu.RLock()
	defer r.mu.RUnlock()

	bs := bitset.New(r.n)
	it := r.bm.Iterator()
	for it.HasNext() {
		bs.Set(uint(it.Next()))
	}

	return bs
}

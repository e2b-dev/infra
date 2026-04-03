package atomicbitset

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/bits-and-blooms/bitset"
)

// Roaring wraps a roaring bitmap with an internal RWMutex.
// The read operations used here (Contains, CardinalityInRange, Iterator/BitSet)
// do not mutate internal state — the array→bitmap conversion that roaring
// can do only happens in getFastContainerAtIndex, which is used by binary
// set operations (And, Or, …), none of which are exposed by this wrapper.
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
	v := r.bm.ContainsInt(int(i))
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
	v := r.bm.CardinalityInRange(uint64(lo), uint64(hi)) == uint64(hi-lo)
	r.mu.RUnlock()

	return v
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

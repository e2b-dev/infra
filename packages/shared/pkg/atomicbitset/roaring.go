package atomicbitset

import (
	"iter"

	"github.com/RoaringBitmap/roaring/v2"
)

// Roaring wraps a roaring bitmap.
// Caller must provide external synchronization for concurrent access.
type Roaring struct {
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

	return r.bm.ContainsInt(int(i))
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

	return r.bm.CardinalityInRange(uint64(lo), uint64(hi)) == uint64(hi-lo)
}

func (r *Roaring) SetRange(lo, hi uint) {
	if hi > r.n {
		hi = r.n
	}
	if lo >= hi {
		return
	}

	r.bm.AddRange(uint64(lo), uint64(hi))
}

func (r *Roaring) Iterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		it := r.bm.Iterator()
		for it.HasNext() {
			if !yield(uint(it.Next())) {
				return
			}
		}
	}
}

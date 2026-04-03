package atomicbitset

import (
	"iter"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
)

// Roaring wraps a roaring bitmap with an internal Mutex.
// A plain Mutex (not RWMutex) is required because many roaring "read"
// operations (CardinalityInRange, Iterator, etc.) may internally mutate
// container representations (e.g. array→bitmap conversion), making them
// unsafe for concurrent use even among readers.
type Roaring struct {
	mu sync.Mutex
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

	r.mu.Lock()
	v := r.bm.ContainsInt(int(i))
	r.mu.Unlock()

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

	r.mu.Lock()
	v := r.bm.CardinalityInRange(uint64(lo), uint64(hi)) == uint64(hi-lo)
	r.mu.Unlock()

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

func (r *Roaring) Iterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		r.mu.Lock()
		defer r.mu.Unlock()

		it := r.bm.Iterator()
		for it.HasNext() {
			if !yield(uint(it.Next())) {
				return
			}
		}
	}
}

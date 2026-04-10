package atomicbitset

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/bits-and-blooms/bitset"
)

var _ Bitset = (*Roaring)(nil)

type Roaring struct {
	mu sync.RWMutex
	bm *roaring.Bitmap
}

func NewRoaring() *Roaring {
	return &Roaring{
		bm: roaring.New(),
	}
}

func (r *Roaring) Has(i uint) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.bm.Contains(uint32(i))
}

func (r *Roaring) HasRange(start, end uint) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i := start; i < end; i++ {
		if !r.bm.Contains(uint32(i)) {
			return false
		}
	}

	return true
}

func (r *Roaring) SetRange(start, end uint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.bm.AddRange(uint64(start), uint64(end))
}

func (r *Roaring) BitSet() *bitset.BitSet {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.bm.ToBitSet()
}

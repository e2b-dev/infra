package atomicbitset

import (
	"sync"

	roaring "github.com/RoaringBitmap/roaring/v2"
	"github.com/bits-and-blooms/bitset"
)

type Bitset struct {
	mu sync.RWMutex
	bm *roaring.Bitmap
}

func New() *Bitset {
	return &Bitset{
		bm: roaring.New(),
	}
}

func (b *Bitset) Has(i uint) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.bm.Contains(uint32(i))
}

func (b *Bitset) HasRange(start, end uint) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if start >= end {
		return true
	}

	rankEnd := b.bm.Rank(uint32(end - 1))
	var rankBefore uint64
	if start > 0 {
		rankBefore = b.bm.Rank(uint32(start - 1))
	}

	return rankEnd-rankBefore == uint64(end-start)
}

func (b *Bitset) SetRange(start, end uint) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bm.AddRange(uint64(start), uint64(end))
}

func (b *Bitset) BitSet() *bitset.BitSet {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.bm.ToBitSet()
}

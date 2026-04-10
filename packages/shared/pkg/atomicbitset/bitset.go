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

func (b *Bitset) HasRange(start, end uint64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.bm.CardinalityInRange(start, end) == end-start
}

func (b *Bitset) SetRange(start, end uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bm.AddRange(start, end)
}

func (b *Bitset) BitSet() *bitset.BitSet {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.bm.ToBitSet()
}

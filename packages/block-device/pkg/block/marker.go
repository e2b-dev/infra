package block

import (
	"sync"

	"github.com/bits-and-blooms/bitset"
)

type Marker struct {
	bitset *bitset.BitSet
	mu     sync.RWMutex
}

// TODO: Not sure if the pre-allocation is necessary.
func NewMarker(size uint) *Marker {
	return &Marker{
		bitset: bitset.New(size),
	}
}

func (b *Marker) Mark(off int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bitset.Set(uint(off))
}

func (b *Marker) IsMarked(off int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.bitset.Test(uint(off))
}

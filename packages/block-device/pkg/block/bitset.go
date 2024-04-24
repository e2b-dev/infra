package block

import (
	"sync"

	"github.com/bits-and-blooms/bitset"
)

type Bitset struct {
	bitset bitset.BitSet
	mu     sync.RWMutex
}

func NewBitset() *Bitset {
	return &Bitset{}
}

func (b *Bitset) Mark(off int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bitset.Set(uint(off))
}

func (b *Bitset) IsMarked(off int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.bitset.Test(uint(off))
}

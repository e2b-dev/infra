package block

import (
	"sync"

	"github.com/bits-and-blooms/bitset"
)

type Bitset struct {
	bitset.BitSet
	mu        sync.RWMutex
	blockSize int64
}

func NewBitset(blockSize int64) *Bitset {
	return &Bitset{
		blockSize: blockSize,
	}
}

func (b *Bitset) Mark(off int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.Set(uint(off / b.blockSize))
}

func (b *Bitset) IsMarked(off int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.Test(uint(off / b.blockSize))
}

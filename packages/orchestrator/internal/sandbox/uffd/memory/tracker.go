package memory

import (
	"sync"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Tracker struct {
	bitset    *bitset.BitSet
	blockSize int64
	mu        sync.RWMutex
}

func NewTracker(size, blockSize int64) *Tracker {
	return &Tracker{
		bitset:    bitset.New(uint(header.TotalBlocks(size, blockSize))),
		blockSize: blockSize,
		mu:        sync.RWMutex{},
	}
}

func (t *Tracker) Mark(offset int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.bitset.Set(uint(header.BlockIdx(offset, t.blockSize)))
}

func (t *Tracker) BitSet() *bitset.BitSet {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.bitset.Clone()
}

func (t *Tracker) Check(offset int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.bitset.Test(uint(header.BlockIdx(offset, t.blockSize)))
}

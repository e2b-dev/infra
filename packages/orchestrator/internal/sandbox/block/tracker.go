package block

import (
	"sync"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Tracker struct {
	b  *bitset.BitSet
	mu sync.RWMutex

	blockSize int64
}

func NewTracker(blockSize int64) *Tracker {
	return &Tracker{
		// The bitset resizes automatically based on the maximum set bit.
		b:         bitset.New(0),
		blockSize: blockSize,
	}
}

func NewTrackerFromBitset(b *bitset.BitSet, blockSize int64) *Tracker {
	return &Tracker{
		b:         b,
		blockSize: blockSize,
	}
}

func (t *Tracker) Has(off int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Test(uint(header.BlockIdx(off, t.blockSize)))
}

func (t *Tracker) Add(off int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.b.Test(uint(header.BlockIdx(off, t.blockSize))) {
		return false
	}

	t.b.Set(uint(header.BlockIdx(off, t.blockSize)))

	return true
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.ClearAll()
}

// BitSet returns a clone of the bitset and the block size.
func (t *Tracker) BitSet() *bitset.BitSet {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Clone()
}

func (t *Tracker) BlockSize() int64 {
	return t.blockSize
}

func (t *Tracker) Clone() *Tracker {
	return &Tracker{
		b:         t.BitSet(),
		blockSize: t.BlockSize(),
	}
}

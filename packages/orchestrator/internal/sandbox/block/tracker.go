package block

import (
	"iter"
	"sync"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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

func (t *Tracker) Has(off int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Test(uint(header.BlockIdx(off, t.blockSize)))
}

func (t *Tracker) Add(off int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.Set(uint(header.BlockIdx(off, t.blockSize)))
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.ClearAll()
}

// BitSet returns the bitset.
// This is not safe to use concurrently.
func (t *Tracker) BitSet() *bitset.BitSet {
	return t.b
}

func (t *Tracker) BlockSize() int64 {
	return t.blockSize
}

func (t *Tracker) Clone() *Tracker {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return &Tracker{
		b:         t.b.Clone(),
		blockSize: t.BlockSize(),
	}
}

func (t *Tracker) Offsets() iter.Seq[int64] {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return bitsetOffsets(t.b.Clone(), t.BlockSize())
}

func bitsetOffsets(b *bitset.BitSet, blockSize int64) iter.Seq[int64] {
	return utils.TransformTo(b.EachSet(), func(idx uint) int64 {
		return header.BlockOffset(int64(idx), blockSize)
	})
}

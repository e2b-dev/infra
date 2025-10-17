package block

import (
	"iter"
	"sync"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Tracker struct {
	bitset *bitset.BitSet
	mu     sync.RWMutex

	blockSize int64
}

func NewTracker(blockSize int64) *Tracker {
	return &Tracker{
		// The bitset resizes automatically based on the maximum set bit.
		bitset:    bitset.New(0),
		blockSize: blockSize,
	}
}

func (t *Tracker) Offsets() iter.Seq[int64] {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return func(yield func(offset int64) bool) {
		for offset := range t.bitset.EachSet() {
			if !yield(header.BlockOffset(int64(offset), t.blockSize)) {
				return
			}
		}
	}
}

func (t *Tracker) Ranges() iter.Seq[Range[int64, int64]] {
	return func(yield func(Range[int64, int64]) bool) {
		for start, size := range BitsetRanges(t.bitset) {
			if !yield(NewBlockRange(int64(start), int64(size), t.blockSize)) {
				return
			}
		}
	}
}

func (t *Tracker) Has(offset int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.bitset.Test(uint(header.BlockIdx(offset, t.blockSize)))
}

func (t *Tracker) Add(offset int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.bitset.Test(uint(header.BlockIdx(offset, t.blockSize))) {
		return false
	}

	t.bitset.Set(uint(header.BlockIdx(offset, t.blockSize)))

	return true
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.bitset.ClearAll()
}

func (t *Tracker) BitSet() *bitset.BitSet {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.bitset.Clone()
}

func (t *Tracker) Clone() *Tracker {
	return &Tracker{
		bitset:    t.BitSet(),
		blockSize: t.blockSize,
	}
}

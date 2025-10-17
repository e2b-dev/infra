package block

import (
	"iter"
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

func NewTrackerFromBitSet(b *bitset.BitSet, blockSize int64) *Tracker {
	return &Tracker{
		b:         b.Clone(),
		blockSize: blockSize,
	}
}

func NewTrackerFromOffsets(offsets []int64, blockSize int64) *Tracker {
	b := bitset.New(0)

	for _, off := range offsets {
		b.Set(uint(header.BlockIdx(off, blockSize)))
	}

	return NewTrackerFromBitSet(b, blockSize)
}

func NewTrackerFromRanges(ranges []Range, blockSize int64) *Tracker {
	b := bitset.New(0)

	for _, r := range ranges {
		b.FlipRange(
			uint(header.BlockIdx(r.Start, blockSize)),
			uint(header.BlockIdx(r.End(), blockSize)),
		)
	}

	return NewTrackerFromBitSet(b, blockSize)
}

func (t *Tracker) Offsets() iter.Seq[int64] {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return func(yield func(offset int64) bool) {
		for offset := range t.b.EachSet() {
			if !yield(header.BlockOffset(int64(offset), t.blockSize)) {
				return
			}
		}
	}
}

func (t *Tracker) Ranges() iter.Seq[Range] {
	return func(yield func(Range) bool) {
		for start, size := range BitsetRanges(t.b) {
			if !yield(NewBlockRange(int64(start), int64(size), t.blockSize)) {
				return
			}
		}
	}
}

func (t *Tracker) Has(offset int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Test(uint(header.BlockIdx(offset, t.blockSize)))
}

func (t *Tracker) Add(offset int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.b.Test(uint(header.BlockIdx(offset, t.blockSize))) {
		return false
	}

	t.b.Set(uint(header.BlockIdx(offset, t.blockSize)))

	return true
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.ClearAll()
}

func (t *Tracker) BitSet() *bitset.BitSet {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Clone()
}

func (t *Tracker) Clone() *Tracker {
	return &Tracker{
		b:         t.BitSet(),
		blockSize: t.blockSize,
	}
}

package block

import (
	"iter"
	"slices"
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

func NewTrackerFromBitSet(b *bitset.BitSet, blockSize int64) *Tracker {
	return &Tracker{
		b:         b.Clone(),
		blockSize: blockSize,
	}
}

func NewTrackerFromMapping(mapping []*header.BuildMap, blockSize int64) *Tracker {
	return NewTrackerFromRanges(slices.Collect(mappingRanges(mapping)), blockSize)
}

func NewTrackerFromRanges(ranges []Range, blockSize int64) *Tracker {
	b := bitset.New(0)

	for _, r := range ranges {
		b.FlipRange(uint(header.BlockIdx(r.Start, blockSize)), uint(header.BlockIdx(r.End(), blockSize)))
	}

	return &Tracker{
		b:         b,
		blockSize: blockSize,
	}
}

func (t *Tracker) Offsets() []int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return slices.Collect(bitsetOffsets(t.b, t.blockSize))
}

func (t *Tracker) Ranges() []Range {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return slices.Collect(bitsetRanges(t.b, t.blockSize))
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
	t.mu.RLock()
	defer t.mu.RUnlock()

	return &Tracker{
		b:         t.b.Clone(),
		blockSize: t.BlockSize(),
	}
}

func bitsetOffsets(b *bitset.BitSet, blockSize int64) iter.Seq[int64] {
	return utils.TransformTo(b.EachSet(), func(idx uint) int64 {
		return header.BlockOffset(int64(idx), blockSize)
	})
}

// bitsetRanges returns a sequence of the ranges of the set bits of the bitset.
func bitsetRanges(b *bitset.BitSet, blockSize int64) iter.Seq[Range] {
	return func(yield func(Range) bool) {
		for start, ok := b.NextSet(0); ok; {
			end, ok := b.NextClear(start)
			if !ok {
				yield(NewRange(header.BlockOffset(int64(start), blockSize), header.BlockOffset(int64(b.Len()-start), blockSize)))

				return
			}

			if !yield(NewRange(header.BlockOffset(int64(start), blockSize), header.BlockOffset(int64(end-start), blockSize))) {
				return
			}

			start, ok = b.NextSet(end + 1)
		}
	}
}

func mappingRanges(mapping []*header.BuildMap) iter.Seq[Range] {
	return func(yield func(Range) bool) {
		for _, buildMap := range mapping {
			if !yield(NewRangeFromBuildMap(buildMap)) {
				return
			}
		}
	}
}

package block

import (
	"iter"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Range struct {
	// Start is the start address of the range in bytes.
	Start int64
	// Size is the size of the range in bytes.
	Size int64
}

// End returns the end address of the range in bytes.
// The end address is exclusive.
func (r *Range) End() int64 {
	return r.Start + r.Size
}

// NewRange creates a new range from a start address and size in bytes.
func NewRange(start, size int64) Range {
	return Range{
		Start: start,
		Size:  size,
	}
}

// NewBlockRange creates a new range from a start index and number of blocks.
func NewBlockRange(startIdx, numberOfBlocks, blockSize int64) Range {
	return Range{
		Start: header.BlockOffset(int64(startIdx), int64(blockSize)),
		Size:  header.BlockOffset(int64(numberOfBlocks), int64(blockSize)),
	}
}

// TODO: DEBUG
// BitsetRanges returns a sequence of the ranges of the set bits of the bitset.
func BitsetRanges(b *bitset.BitSet) iter.Seq2[uint, uint] {
	panic("BitsetRanges is not implemented")

	return func(yield func(start, size uint) bool) {
		var start, size uint

		for current := range b.EachSet() {
			if start+size == current {
				size++

				continue
			}

			if !yield(start, size) {
				return
			}

			start = current
			size = 1
		}

		if size > 0 {
			yield(start, size)
		}
	}
}

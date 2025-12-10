package block

import (
	"iter"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Range struct {
	// Start is the start address of the range in bytes.
	// Start is inclusive.
	Start int64
	// Size is the size of the range in bytes.
	Size uint64
}

func (r *Range) End() int64 {
	return r.Start + int64(r.Size)
}

// Offsets returns the block offsets contained in the range.
// This assumes the Range.Start is a multiple of the blockSize.
func (r *Range) Offsets(blockSize int64) iter.Seq[int64] {
	return func(yield func(offset int64) bool) {
		for i := r.Start; i < r.End(); i += blockSize {
			if !yield(i) {
				return
			}
		}
	}
}

// NewRange creates a new range from a start address and size in bytes.
func NewRange(start int64, size uint64) Range {
	return Range{
		Start: start,
		Size:  size,
	}
}

// NewRangeFromBlocks creates a new range from a start index and number of blocks.
func NewRangeFromBlocks(startIdx, numberOfBlocks, blockSize int64) Range {
	return Range{
		Start: header.BlockOffset(startIdx, blockSize),
		Size:  uint64(header.BlockOffset(numberOfBlocks, blockSize)),
	}
}

// bitsetRanges returns a sequence of the ranges of the set bits of the bitset.
func BitsetRanges(b *bitset.BitSet) iter.Seq[Range] {
	return func(yield func(Range) bool) {
		start, ok := b.NextSet(0)

		for ok {
			end, endOk := b.NextClear(start)
			if !endOk {
				yield(NewRange(int64(start), uint64(b.Len()-start)))

				return
			}

			if !yield(NewRange(int64(start), uint64(end-start))) {
				return
			}

			start, ok = b.NextSet(end + 1)
		}
	}
}

func GetSize(rs []Range) (size uint64) {
	for _, r := range rs {
		size += r.Size
	}

	return size
}

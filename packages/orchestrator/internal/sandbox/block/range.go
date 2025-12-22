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
	Size int64
}

func (r *Range) End() int64 {
	return r.Start + r.Size
}

// Offsets returns the block offsets contained in the range.
// This assumes the Range.Start is a multiple of the blockSize.
func (r *Range) Offsets(blockSize int64) iter.Seq[int64] {
	return func(yield func(offset int64) bool) {
		getOffsets(r.Start, r.End(), blockSize)(yield)
	}
}

func getOffsets(start, end int64, blockSize int64) iter.Seq[int64] {
	return func(yield func(offset int64) bool) {
		for off := start; off < end; off += blockSize {
			if !yield(off) {
				return
			}
		}
	}
}

// NewRange creates a new range from a start address and size in bytes.
func NewRange(start int64, size int64) Range {
	return Range{
		Start: start,
		Size:  size,
	}
}

// NewRangeFromBlocks creates a new range from a start index and number of blocks.
func NewRangeFromBlocks(startIdx, numberOfBlocks, blockSize int64) Range {
	return Range{
		Start: header.BlockOffset(startIdx, blockSize),
		Size:  header.BlockOffset(numberOfBlocks, blockSize),
	}
}

// bitsetRanges returns a sequence of the ranges of the set bits of the bitset.
func BitsetRanges(b *bitset.BitSet, blockSize int64) iter.Seq[Range] {
	return func(yield func(Range) bool) {
		start, found := b.NextSet(0)

		for found {
			end, endOk := b.NextClear(start)
			if !endOk {
				yield(NewRangeFromBlocks(int64(start), int64(b.Len()-start), blockSize))

				return
			}

			if !yield(NewRangeFromBlocks(int64(start), int64(end-start), blockSize)) {
				return
			}

			start, found = b.NextSet(end + 1)
		}
	}
}

func GetSize(rs []Range) (size int64) {
	for _, r := range rs {
		size += r.Size
	}

	return size
}

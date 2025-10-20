package block

import (
	"iter"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Range struct {
	// Start is the start address of the range in bytes.
	// Start is inclusive.
	Start int64
	// Size is the size of the range in bytes.
	Size int64
}

// NewRange creates a new range from a start address and size in bytes.
func NewRange(start, size int64) Range {
	return Range{
		Start: start,
		Size:  size,
	}
}

func NewRangeFromBuildMap(b *header.BuildMap) Range {
	return Range{
		Start: int64(b.Offset),
		Size:  int64(b.Length),
	}
}

// NewRangeFromBlocks creates a new range from a start index and number of blocks.
func NewRangeFromBlocks(startIdx, numberOfBlocks, blockSize int64) Range {
	return Range{
		Start: header.BlockOffset(startIdx, blockSize),
		Size:  header.BlockOffset(numberOfBlocks, blockSize),
	}
}

// End returns the end address of the range in bytes.
// The end address is exclusive.
func (r *Range) End() int64 {
	return r.Start + r.Size
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

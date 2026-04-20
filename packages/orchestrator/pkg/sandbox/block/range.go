package block

import (
	"iter"

	"github.com/RoaringBitmap/roaring/v2"

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

// BitsetRanges returns a sequence of the ranges of the set bits of the bitmap.
func BitsetRanges(b *roaring.Bitmap, blockSize int64) iter.Seq[Range] {
	return func(yield func(Range) bool) {
		for start, endExcl := range b.Ranges() {
			if !yield(NewRangeFromBlocks(int64(start), int64(endExcl)-int64(start), blockSize)) {
				return
			}
		}
	}
}

func GetSize(rs []Range) (size int64) {
	for _, r := range rs {
		size += r.Size
	}

	return size
}

package block

import (
	"iter"

	"github.com/bits-and-blooms/bitset"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Address interface {
	~int64 | ~uint64 | ~int | ~uint | ~uintptr
}

type Range[T Address, U Address] struct {
	// Start is the start address of the range in bytes.
	Start T
	// Size is the size of the range in bytes.
	Size U
}

// NewRange creates a new range from a start address and size in bytes.
func NewRange[T Address, U Address](start T, size U) Range[T, U] {
	return Range[T, U]{
		Start: start,
		Size:  size,
	}
}

// NewBlockRange creates a new range from a start index and number of blocks.
func NewBlockRange[T Address, U Address](startIdx, numberOfBlocks T, blockSize U) Range[T, U] {
	return Range[T, U]{
		Start: T(header.BlockOffset(int64(startIdx), int64(blockSize))),
		Size:  U(header.BlockOffset(int64(numberOfBlocks), int64(blockSize))),
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

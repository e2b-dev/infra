package atomicbitset

import (
	"iter"

	"github.com/bits-and-blooms/bitset"
)

// BitsAndBlooms wraps a bits-and-blooms/bitset.BitSet.
// Caller must provide external synchronization for concurrent access.
type BitsAndBlooms struct {
	bs *bitset.BitSet
	n  uint
}

func NewBitsAndBlooms(n uint) *BitsAndBlooms {
	return &BitsAndBlooms{
		bs: bitset.New(n),
		n:  n,
	}
}

func (b *BitsAndBlooms) Len() uint { return b.n }

func (b *BitsAndBlooms) Has(i uint) bool {
	if i >= b.n {
		return false
	}

	return b.bs.Test(i)
}

func (b *BitsAndBlooms) HasRange(lo, hi uint) bool {
	if lo >= hi {
		return true
	}
	if hi > b.n {
		hi = b.n
	}
	if lo >= hi {
		return false
	}

	for i := lo; i < hi; i++ {
		if !b.bs.Test(i) {
			return false
		}
	}

	return true
}

func (b *BitsAndBlooms) SetRange(lo, hi uint) {
	if hi > b.n {
		hi = b.n
	}
	if lo >= hi {
		return
	}

	for i := lo; i < hi; i++ {
		b.bs.Set(i)
	}
}

func (b *BitsAndBlooms) Iterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		for i, ok := b.bs.NextSet(0); ok; i, ok = b.bs.NextSet(i + 1) {
			if !yield(i) {
				return
			}
		}
	}
}

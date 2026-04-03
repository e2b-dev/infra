package atomicbitset

import (
	"iter"
	"sync"

	"github.com/bits-and-blooms/bitset"
)

// BitsAndBlooms wraps a bits-and-blooms/bitset.BitSet with an internal RWMutex.
type BitsAndBlooms struct {
	mu sync.RWMutex
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

	b.mu.RLock()
	v := b.bs.Test(i)
	b.mu.RUnlock()

	return v
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

	b.mu.RLock()
	defer b.mu.RUnlock()

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

	b.mu.Lock()
	for i := lo; i < hi; i++ {
		b.bs.Set(i)
	}
	b.mu.Unlock()
}

func (b *BitsAndBlooms) Iterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		b.mu.RLock()
		defer b.mu.RUnlock()

		for i, ok := b.bs.NextSet(0); ok; i, ok = b.bs.NextSet(i + 1) {
			if !yield(i) {
				return
			}
		}
	}
}

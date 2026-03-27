// Package atomicbitset provides a fixed-size bitset with atomic set operations.
package atomicbitset

import (
	"iter"
	"math"
	"math/bits"
	"sync/atomic"
)

// Bitset is a fixed-size bitset backed by atomic uint64 words.
// SetRange uses atomic OR, so concurrent writers are safe without
// external locking.
//
// A Bitset must not be copied after first use (copies share the
// underlying array).
type Bitset struct {
	words []atomic.Uint64
	n     uint
}

// New returns a Bitset with capacity for n bits, all initially zero.
func New(n uint) Bitset {
	return Bitset{
		words: make([]atomic.Uint64, (n+63)/64),
		n:     n,
	}
}

// Len returns the capacity in bits.
func (b *Bitset) Len() uint { return b.n }

// Has reports whether bit i is set. Out-of-range returns false.
func (b *Bitset) Has(i uint) bool {
	if i >= b.n {
		return false
	}

	return b.words[i/64].Load()&(1<<(i%64)) != 0
}

// wordMask returns a bitmask covering bits [lo, hi) within a single uint64 word.
func wordMask(lo, hi uint) uint64 {
	if hi-lo == 64 {
		return math.MaxUint64
	}

	return ((1 << (hi - lo)) - 1) << lo
}

// HasRange reports whether every bit in [lo, hi) is set.
// An empty range returns true. hi is capped to Len().
// Returns false if lo is out of range and the range is non-empty.
func (b *Bitset) HasRange(lo, hi uint) bool {
	if lo >= hi {
		return true
	}
	if hi > b.n {
		hi = b.n
	}
	if lo >= hi {
		return false
	}
	for i := lo; i < hi; {
		w := i / 64
		bit := i % 64
		top := min(hi-w*64, 64)
		mask := wordMask(bit, top)

		if b.words[w].Load()&mask != mask {
			return false
		}
		i = (w + 1) * 64
	}

	return true
}

// SetRange sets every bit in [lo, hi) using atomic OR.
// hi is capped to Len().
func (b *Bitset) SetRange(lo, hi uint) {
	if hi > b.n {
		hi = b.n
	}
	if lo >= hi {
		return
	}
	for i := lo; i < hi; {
		w := i / 64
		bit := i % 64
		top := min(hi-w*64, 64)

		b.words[w].Or(wordMask(bit, top))

		i = (w + 1) * 64
	}
}

// Iterator returns an iterator over the indices of set bits
// in ascending order.
func (b *Bitset) Iterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		for wi := range b.words {
			word := b.words[wi].Load()
			base := uint(wi) * 64
			for word != 0 {
				tz := uint(bits.TrailingZeros64(word))
				if !yield(base + tz) {
					return
				}
				word &= word - 1
			}
		}
	}
}

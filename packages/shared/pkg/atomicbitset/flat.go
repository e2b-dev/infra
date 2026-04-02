package atomicbitset

import (
	"iter"
	"math"
	"math/bits"
	"sync/atomic"
)

type Flat struct {
	words []atomic.Uint64
	n     uint
}

func NewFlat(n uint) *Flat {
	return &Flat{
		words: make([]atomic.Uint64, (n+63)/64),
		n:     n,
	}
}

func (b *Flat) Len() uint { return b.n }

func (b *Flat) Has(i uint) bool {
	if i >= b.n {
		return false
	}

	wordIndex := i / 64
	bitIndex := i % 64

	mask := uint64(1) << bitIndex

	return b.words[wordIndex].Load()&mask != 0
}

func (b *Flat) HasRange(lo, hi uint) bool {
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

func (b *Flat) SetRange(lo, hi uint) {
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

func (b *Flat) Iterator() iter.Seq[uint] {
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

func wordMask(lo, hi uint) uint64 {
	if hi-lo == 64 {
		return math.MaxUint64
	}

	return ((1 << (hi - lo)) - 1) << lo
}

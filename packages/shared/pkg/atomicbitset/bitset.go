// Package atomicbitset provides fixed-size bitset implementations.
// All implementations are safe for concurrent HasRange and SetRange.
package atomicbitset

import (
	"fmt"
	"iter"
)

type Bitset interface {
	Has(i uint) bool
	HasRange(lo, hi uint) bool
	SetRange(lo, hi uint)
	Iterator() iter.Seq[uint]
	UnsafeIterator() iter.Seq[uint]
	Len() uint
}

const (
	autoThreshold uint = 524_288 // 64 KB flat bitmap

	// Valid impl values for New.
	BitsetDefault = ""
	BitsetRoaring = "roaring"
	BitsetAtomic  = "atomic"
)

func New(n uint, impl string) Bitset {
	switch impl {
	case BitsetDefault, BitsetRoaring:
		return NewRoaring(n)
	case BitsetAtomic:
		if n <= autoThreshold {
			return NewFlat(n)
		}

		return NewSharded(n, DefaultShardBits)
	default:
		panic(fmt.Sprintf("atomicbitset: unknown implementation %q", impl))
	}
}

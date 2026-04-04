// Package atomicbitset provides fixed-size bitset implementations.
// All implementations are safe for concurrent HasRange and SetRange.
package atomicbitset

import (
	"fmt"

	"github.com/bits-and-blooms/bitset"
)

type Bitset interface {
	Has(i uint) bool
	HasRange(lo, hi uint) bool
	SetRange(lo, hi uint)
	BitSet() *bitset.BitSet
	Len() uint
}

const (
	autoThreshold uint = 524_288 // 64 KB flat bitmap

	// Valid impl values for New.
	BitsetDefault       = ""
	BitsetRoaring       = "roaring"
	BitsetRoaring64     = "roaring64"
	BitsetAtomic        = "atomic"
	BitsetBitsAndBlooms = "bits-and-blooms"
	BitsetSyncMap       = "syncmap"
)

func New(n uint, impl string) Bitset {
	switch impl {
	case BitsetDefault, BitsetRoaring:
		return NewRoaring(n)
	case BitsetRoaring64:
		return NewRoaring64(n)
	case BitsetBitsAndBlooms:
		return NewBitsAndBlooms(n)
	case BitsetAtomic:
		if n <= autoThreshold {
			return NewFlat(n)
		}

		return NewSharded(n, DefaultShardBits)
	case BitsetSyncMap:
		return NewSyncMap(n)
	default:
		panic(fmt.Sprintf("atomicbitset: unknown implementation %q", impl))
	}
}

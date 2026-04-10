// Package atomicbitset provides fixed-size bitset implementations.
// All implementations are safe for concurrent HasRange and SetRange.
package atomicbitset

import (
	"github.com/bits-and-blooms/bitset"
)

type Bitset interface {
	Has(i uint) bool
	HasRange(lo, hi uint) bool
	SetRange(lo, hi uint)
	BitSet() *bitset.BitSet
}

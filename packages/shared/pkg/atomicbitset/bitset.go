package atomicbitset

import (
	"github.com/bits-and-blooms/bitset"
)

type Bitset interface {
	Has(i uint) bool
	HasRange(start, end uint) bool
	SetRange(start, end uint)
	BitSet() *bitset.BitSet
}

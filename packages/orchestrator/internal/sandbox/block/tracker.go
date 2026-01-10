package block

import (
	"iter"
	"maps"
	"sync"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// AccessType represents the type of access that caused a block to be loaded.
type AccessType string

const (
	// Read indicates a block loaded by a read operation.
	Read AccessType = "read"
	// Write indicates a block loaded by a write operation.
	Write AccessType = "write"
	// Prefetch indicates a proactively prefetched block, not a real fault.
	Prefetch AccessType = "prefetch"
)

// BlockEntry holds metadata about a tracked block.
type BlockEntry struct {
	Index      uint64
	Order      uint64
	AccessType AccessType
}

type Tracker struct {
	b  *bitset.BitSet
	mu sync.RWMutex

	blockSize int64

	// blockEntries stores metadata for each block index
	blockEntries map[uint64]BlockEntry
	// orderCounter tracks the next order number to assign
	orderCounter uint64
}

func NewTracker(blockSize int64) *Tracker {
	return &Tracker{
		// The bitset resizes automatically based on the maximum set bit.
		b:            bitset.New(0),
		blockSize:    blockSize,
		blockEntries: make(map[uint64]BlockEntry),
		orderCounter: 1,
	}
}

func NewTrackerFromBitset(b *bitset.BitSet, blockSize int64) *Tracker {
	return &Tracker{
		b:            b,
		blockSize:    blockSize,
		blockEntries: make(map[uint64]BlockEntry),
		orderCounter: 1,
	}
}

func (t *Tracker) Has(off int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Test(uint(header.BlockIdx(off, t.blockSize)))
}

// Add adds an offset to the tracker with metadata about the access.
func (t *Tracker) Add(off int64, accessType AccessType) {
	t.mu.Lock()
	defer t.mu.Unlock()

	idx := uint64(header.BlockIdx(off, t.blockSize))

	// Only add if not already tracked
	if !t.b.Test(uint(idx)) {
		t.b.Set(uint(idx))
		t.blockEntries[idx] = BlockEntry{
			Index:      idx,
			Order:      t.orderCounter,
			AccessType: accessType,
		}
		t.orderCounter++
	}
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.ClearAll()
	t.blockEntries = make(map[uint64]BlockEntry)
	t.orderCounter = 1
}

// BitSet returns the bitset.
// This is not safe to use concurrently.
func (t *Tracker) BitSet() *bitset.BitSet {
	return t.b
}

func (t *Tracker) BlockSize() int64 {
	return t.blockSize
}

// BlockEntries returns a copy of the block entries map.
func (t *Tracker) BlockEntries() map[uint64]BlockEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[uint64]BlockEntry, len(t.blockEntries))
	maps.Copy(result, t.blockEntries)

	return result
}

func (t *Tracker) Clone() *Tracker {
	t.mu.RLock()
	defer t.mu.RUnlock()

	blockEntries := make(map[uint64]BlockEntry, len(t.blockEntries))
	maps.Copy(blockEntries, t.blockEntries)

	return &Tracker{
		b:            t.b.Clone(),
		blockSize:    t.BlockSize(),
		blockEntries: blockEntries,
		orderCounter: t.orderCounter,
	}
}

func (t *Tracker) Offsets() iter.Seq[int64] {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return bitsetOffsets(t.b.Clone(), t.BlockSize())
}

func bitsetOffsets(b *bitset.BitSet, blockSize int64) iter.Seq[int64] {
	return utils.TransformTo(b.EachSet(), func(idx uint) int64 {
		return header.BlockOffset(int64(idx), blockSize)
	})
}

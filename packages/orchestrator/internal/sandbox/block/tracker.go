package block

import (
	"iter"
	"sync"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// FaultType represents the type of memory access that caused a page to be loaded.
type FaultType string

const (
	// FaultTypeRead indicates a page fault caused by a read operation
	FaultTypeRead FaultType = "read"
	// FaultTypeWrite indicates a page fault caused by a write operation
	FaultTypeWrite FaultType = "write"
	// FaultTypePrefault indicates a proactive prefetch, not a real page fault
	FaultTypePrefault FaultType = "prefault"
)

// PageEntry holds metadata about a tracked page.
type PageEntry struct {
	Index     uint64
	Order     uint64
	FaultType FaultType
}

type Tracker struct {
	b  *bitset.BitSet
	mu sync.RWMutex

	blockSize int64

	// pageEntries stores metadata for each block index
	pageEntries map[uint64]PageEntry
	// orderCounter tracks the next order number to assign
	orderCounter uint64
}

func NewTracker(blockSize int64) *Tracker {
	return &Tracker{
		// The bitset resizes automatically based on the maximum set bit.
		b:            bitset.New(0),
		blockSize:    blockSize,
		pageEntries:  make(map[uint64]PageEntry),
		orderCounter: 1,
	}
}

func NewTrackerFromBitset(b *bitset.BitSet, blockSize int64) *Tracker {
	return &Tracker{
		b:            b,
		blockSize:    blockSize,
		pageEntries:  make(map[uint64]PageEntry),
		orderCounter: 1,
	}
}

func (t *Tracker) Has(off int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Test(uint(header.BlockIdx(off, t.blockSize)))
}

// Add adds an offset to the tracker with metadata about the access.
func (t *Tracker) Add(off int64, faultType FaultType) {
	t.mu.Lock()
	defer t.mu.Unlock()

	idx := uint64(header.BlockIdx(off, t.blockSize))

	// Only add if not already tracked
	if !t.b.Test(uint(idx)) {
		t.b.Set(uint(idx))
		t.pageEntries[idx] = PageEntry{
			Index:     idx,
			Order:     t.orderCounter,
			FaultType: faultType,
		}
		t.orderCounter++
	}
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.ClearAll()
	t.pageEntries = make(map[uint64]PageEntry)
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

// PageEntries returns a copy of the page entries map.
func (t *Tracker) PageEntries() map[uint64]PageEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[uint64]PageEntry, len(t.pageEntries))
	for k, v := range t.pageEntries {
		result[k] = v
	}

	return result
}

func (t *Tracker) Clone() *Tracker {
	t.mu.RLock()
	defer t.mu.RUnlock()

	pageEntries := make(map[uint64]PageEntry, len(t.pageEntries))
	for k, v := range t.pageEntries {
		pageEntries[k] = v
	}

	return &Tracker{
		b:            t.b.Clone(),
		blockSize:    t.BlockSize(),
		pageEntries:  pageEntries,
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

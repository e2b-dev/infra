package block

import (
	"maps"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// PrefetchData contains block access data for prefetch mapping.
type PrefetchData struct {
	// BlockEntries contains metadata for each block index
	BlockEntries map[uint64]PrefetchBlockEntry
	// BlockSize is the size of each block in bytes
	BlockSize int64
}

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
type PrefetchBlockEntry struct {
	Index      uint64
	Order      uint64
	AccessType AccessType
}

type PrefetchTracker struct {
	mu sync.RWMutex

	blockSize int64

	// blockEntries stores metadata for each block index
	blockEntries map[uint64]PrefetchBlockEntry
	// orderCounter tracks the next order number to assign
	orderCounter uint64

	isTracking bool
}

func NewPrefetchTracker(blockSize int64) *PrefetchTracker {
	return &PrefetchTracker{
		blockSize:    blockSize,
		blockEntries: make(map[uint64]PrefetchBlockEntry),
		orderCounter: 1,
		isTracking:   true,
	}
}

// Add adds an offset to the tracker with metadata about the access.
func (t *PrefetchTracker) Add(off int64, accessType AccessType) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.isTracking {
		return
	}

	idx := uint64(header.BlockIdx(off, t.blockSize))

	// Only add if not already tracked
	if _, ok := t.blockEntries[idx]; !ok {
		t.blockEntries[idx] = PrefetchBlockEntry{
			Index:      idx,
			Order:      t.orderCounter,
			AccessType: accessType,
		}
		t.orderCounter++
	}
}

func (t *PrefetchTracker) PrefetchData() PrefetchData {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[uint64]PrefetchBlockEntry, len(t.blockEntries))
	maps.Copy(result, t.blockEntries)

	// Stop tracking new blocks
	t.isTracking = false

	return PrefetchData{
		BlockEntries: result,
		BlockSize:    t.blockSize,
	}
}

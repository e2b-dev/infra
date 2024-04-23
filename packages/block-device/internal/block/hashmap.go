package block

import (
	"sync"
)

type HashMap struct {
	mu   sync.RWMutex
	data map[uint32]struct{}

	blockSize int64
}

func NewHashMap(blockSize int64) *HashMap {
	return &HashMap{
		blockSize: blockSize,
		data:      make(map[uint32]struct{}),
	}
}

func (h *HashMap) Mark(off int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.data[uint32(off/h.blockSize)] = struct{}{}
}

func (h *HashMap) IsMarked(off int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	_, ok := h.data[uint32(off/h.blockSize)]
	return ok
}

package testutils

import "sync"

// ContiguousMap is a mapping that is contiguous in the host virtual address space.
// This is used for testing purposes.
type ContiguousMap struct {
	start    uintptr
	size     uint64
	pagesize uint64

	accessedOffsets []uintptr
	mu              sync.RWMutex
}

func NewContiguousMap(start uintptr, size, pagesize uint64) *ContiguousMap {
	return &ContiguousMap{
		start:    start,
		size:     size,
		pagesize: pagesize,
	}
}

func (m *ContiguousMap) GetOffset(addr uintptr) (int64, uint64, error) {
	offset := addr - m.start
	pagesize := m.pagesize

	m.mu.Lock()
	m.accessedOffsets = append(m.accessedOffsets, offset)
	m.mu.Unlock()

	return int64(offset), pagesize, nil
}

func (m *ContiguousMap) Accessed() []uintptr {
	m.mu.RLock()
	defer m.mu.RUnlock()

	accessedOffsets := make([]uintptr, len(m.accessedOffsets))
	copy(accessedOffsets, m.accessedOffsets)

	return accessedOffsets
}

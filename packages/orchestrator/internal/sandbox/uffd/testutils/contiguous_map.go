package testutils

import (
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// ContiguousMap is a mapping that is contiguous in the host virtual address space.
// This is used for testing purposes.
type ContiguousMap struct {
	start    uintptr
	size     uint64
	pagesize uint64

	accessedOffsets map[uint64]struct{}
	mu              sync.RWMutex
}

func NewContiguousMap(start uintptr, size, pagesize uint64) *ContiguousMap {
	return &ContiguousMap{
		start:           start,
		size:            size,
		pagesize:        pagesize,
		accessedOffsets: make(map[uint64]struct{}),
	}
}

func (m *ContiguousMap) GetOffset(addr uintptr) (int64, uint64, error) {
	offset := addr - m.start
	pagesize := m.pagesize

	m.mu.Lock()
	m.accessedOffsets[uint64(offset)] = struct{}{}
	m.mu.Unlock()

	return int64(offset), pagesize, nil
}

func (m *ContiguousMap) Map() map[uint64]struct{} {
	return m.accessedOffsets
}

func (m *ContiguousMap) Keys() []uint64 {
	return utils.MapKeys(m.accessedOffsets)
}

func (m *ContiguousMap) GetHostVirtAddr(offset int64) (int64, uint64, error) {
	return int64(m.start) + offset, m.pagesize, nil
}

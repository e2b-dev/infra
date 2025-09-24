package memory

// ContiguousMap is a mapping that is contiguous in the host virtual address space.
// This is used for testing purposes.
type ContiguousMap struct {
	start    uintptr
	size     uint64
	pagesize uint64
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

	return int64(offset), pagesize, nil
}

func (m *ContiguousMap) GetHostVirtAddr(offset int64) (int64, uint64, error) {
	return int64(m.start) + offset, m.pagesize, nil
}

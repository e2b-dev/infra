package memory

type MemoryMap interface {
	GetOffset(hostVirtAddr uintptr) (offset int64, pagesize uint64, err error)
}

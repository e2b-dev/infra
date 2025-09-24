package memory

type MemoryMap interface {
	GetOffset(hostVirtAddr uintptr) (offset int64, pagesize uint64, err error)
	GetHostVirtAddr(offset int64) (hostVirtAddr int64, pagesize uint64, err error)
}

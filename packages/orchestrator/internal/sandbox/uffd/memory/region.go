package memory

// Region is a mapping of a region of memory in the guest to a region of memory on the host.
// The serialization is based on the Firecracker UFFD protocol communication.
type Region struct {
	BaseHostVirtAddr uintptr `json:"base_host_virt_addr"`
	Size             uintptr `json:"size"`
	Offset           uintptr `json:"offset"`
	// This is actually in bytes.
	// This field is deprecated in the newer version of the Firecracer with a new field `page_size`.
	PageSize uintptr `json:"page_size_kib"`
}

// End returns the end address of the region in bytes.
// The end address is exclusive.
func (r *Region) endOffset() int64 {
	return int64(r.Offset + r.Size)
}

// endHostVirtAddr returns the end address of the region in host virtual address.
// The end address is exclusive.
func (r *Region) endHostVirtAddr() uintptr {
	return r.BaseHostVirtAddr + r.Size
}

// shiftedOffset returns the offset of the given address in the region.
func (r *Region) shiftedOffset(addr uintptr) int64 {
	return int64(addr - r.BaseHostVirtAddr + r.Offset)
}

// shiftedHostVirtAddr returns the host virtual address of the given offset in the region.
func (r *Region) shiftedHostVirtAddr(off int64) uintptr {
	return uintptr(off) + r.BaseHostVirtAddr - r.Offset
}

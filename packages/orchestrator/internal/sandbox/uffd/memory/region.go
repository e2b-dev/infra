package memory

// Region is a mapping of a region of memory of the guest to a region of memory on the host.
// The serialization is based on the Firecracker UFFD protocol communication.
// https://github.com/firecracker-microvm/firecracker/blob/ceeca6a14284537ae0b2a192cd2ffef10d3a81e2/src/vmm/src/persist.rs#L96
type Region struct {
	BaseHostVirtAddr uintptr `json:"base_host_virt_addr"`
	Size             uintptr `json:"size"`
	Offset           uintptr `json:"offset"`
	// This field is deprecated in the newer version of the Firecracker with a new field `page_size`.
	PageSize uintptr `json:"page_size_kib"` // This is actually in bytes in the deprecated version.
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

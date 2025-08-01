package firecracker

import "fmt"

type Mappings interface {
	GetRange(addr uintptr) (offset int64, pagesize int64, err error)
}

type GuestRegionUffdMapping struct {
	BaseHostVirtAddr uintptr `json:"base_host_virt_addr"`
	Size             uintptr `json:"size"`
	Offset           uintptr `json:"offset"`
	// This is actually in bytes—it is deprecated and they introduced "page_size"
	PageSize uintptr `json:"page_size_kib"`
}

type FcMappings []GuestRegionUffdMapping

func (m *GuestRegionUffdMapping) relativeOffset(addr uintptr) int64 {
	return int64(m.Offset + addr - m.BaseHostVirtAddr)
}

func (m FcMappings) GetRange(addr uintptr) (int64, int64, error) {
	return getMappedRange(addr, m)
}

// Returns the relative offset and the page size of the mapped range for a given address
func getMappedRange(addr uintptr, mappings []GuestRegionUffdMapping) (int64, int64, error) {
	for _, m := range mappings {
		if addr < m.BaseHostVirtAddr || m.BaseHostVirtAddr+m.Size <= addr {
			// Outside of this mapping
			continue
		}

		return m.relativeOffset(addr), int64(m.PageSize), nil
	}

	return 0, 0, fmt.Errorf("address %d not found in any mapping", addr)
}

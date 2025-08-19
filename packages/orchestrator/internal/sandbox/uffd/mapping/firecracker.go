package mapping

import "fmt"

type GuestRegionUffdMapping struct {
	BaseHostVirtAddr uintptr `json:"base_host_virt_addr"`
	Size             uintptr `json:"size"`
	Offset           uintptr `json:"offset"`
	// This is actually in bytes.
	// This field is deprecated in the newer version of the Firecracer with a new field `page_size`.
	PageSize uintptr `json:"page_size_kib"`
}

func (m *GuestRegionUffdMapping) relativeOffset(addr uintptr) uint64 {
	return uint64(m.Offset + addr - m.BaseHostVirtAddr)
}

type FcMappings []GuestRegionUffdMapping

// Returns the relative offset and the page size of the mapped range for a given address
func (m FcMappings) GetRange(addr uintptr) (uint64, uint64, error) {
	for _, m := range m {
		if addr < m.BaseHostVirtAddr || m.BaseHostVirtAddr+m.Size <= addr {
			// Outside of this mapping
			continue
		}

		return m.relativeOffset(addr), uint64(m.PageSize), nil
	}

	return 0, 0, fmt.Errorf("address %d not found in any mapping", addr)
}

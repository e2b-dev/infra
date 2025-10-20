package memory

import (
	"fmt"
)

type Mapping struct {
	Regions []Region
}

func NewMapping(regions []Region) *Mapping {
	return &Mapping{Regions: regions}
}

// GetOffset returns the relative offset and the page size of the mapped range for a given address.
func (m *Mapping) GetOffset(hostVirtAddr uintptr) (int64, uint64, error) {
	for _, r := range m.Regions {
		if hostVirtAddr >= r.BaseHostVirtAddr && hostVirtAddr < r.endHostVirtAddr() {
			return r.shiftedOffset(hostVirtAddr), uint64(r.PageSize), nil
		}
	}

	return 0, 0, fmt.Errorf("address %d not found in any mapping", hostVirtAddr)
}

// GetHostVirtAddr returns the host virtual address for a given offset.
func (m *Mapping) GetHostVirtAddr(off int64) (int64, uint64, error) {
	r, err := m.getHostVirtRegion(off)
	if err != nil {
		return 0, 0, err
	}

	return int64(r.shiftedHostVirtAddr(off)), uint64(r.PageSize), nil
}

// getHostVirtRegion returns the region that contains the given offset.
func (m *Mapping) getHostVirtRegion(off int64) (*Region, error) {
	for _, r := range m.Regions {
		if off >= int64(r.Offset) && off < r.endOffset() {
			return &r, nil
		}
	}

	return nil, fmt.Errorf("offset %d not found in any mapping", off)
}

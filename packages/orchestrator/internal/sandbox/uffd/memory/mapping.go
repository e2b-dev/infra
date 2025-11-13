package memory

import (
	"fmt"
)

type AddressNotFoundError struct {
	hostVirtAddr uintptr
}

func (e AddressNotFoundError) Error() string {
	return fmt.Sprintf("address %d not found in any mapping", e.hostVirtAddr)
}

type Mapping struct {
	Regions []Region
}

func NewMapping(regions []Region) *Mapping {
	return &Mapping{Regions: regions}
}

// GetOffset returns the relative offset and the page size of the mapped range for a given address.
func (m *Mapping) GetOffset(hostVirtAddr uintptr) (int64, uintptr, error) {
	for _, r := range m.Regions {
		if hostVirtAddr >= r.BaseHostVirtAddr && hostVirtAddr < r.endHostVirtAddr() {
			return r.shiftedOffset(hostVirtAddr), r.PageSize, nil
		}
	}

	return 0, 0, AddressNotFoundError{hostVirtAddr: hostVirtAddr}
}

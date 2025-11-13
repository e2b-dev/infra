package memory

import (
	"fmt"
)

type AddressNotFoundError struct {
	hostVirtAddr uintptr
}

func (e AddressNotFoundError) Error() string {
	return fmt.Sprintf("host virtual address %d not found in any mapping", e.hostVirtAddr)
}

type OffsetNotFoundError struct {
	offset int64
}

func (e OffsetNotFoundError) Error() string {
	return fmt.Sprintf("offset %d not found in any mapping", e.offset)
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

// GetHostVirtRanges returns the host virtual addresses and sizes (ranges) that cover exactly the given [offset, offset+length) range in the host virtual address space.
func (m *Mapping) GetHostVirtAddr(off int64) (uintptr, uintptr, error) {
	for _, r := range m.Regions {
		if off >= int64(r.Offset) && off < r.endOffset() {
			return r.shiftedHostVirtAddr(off), uintptr(r.endOffset()) - r.Offset, nil
		}
	}

	return 0, 0, OffsetNotFoundError{offset: off}
}

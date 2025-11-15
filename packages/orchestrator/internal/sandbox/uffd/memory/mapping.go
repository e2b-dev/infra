package memory

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
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
	Regions  []Region
	pageSize int64
}

func NewMapping(regions []Region) (*Mapping, error) {
	if len(regions) == 0 {
		return nil, fmt.Errorf("no regions provided")
	}

	expectedPageSize := regions[0].PageSize

	for i, region := range regions {
		if region.PageSize != expectedPageSize {
			return nil, fmt.Errorf("page size mismatch: region at index %d has page size %d, expected %d", i, region.PageSize, expectedPageSize)
		}
	}

	return &Mapping{Regions: regions, pageSize: int64(expectedPageSize)}, nil
}

// GetOffset returns the relative offset and the pagesize of the mapped range for a given address.
func (m *Mapping) GetOffset(hostVirtAddr uintptr) (int64, uintptr, error) {
	for _, r := range m.Regions {
		if hostVirtAddr >= r.BaseHostVirtAddr && hostVirtAddr < r.endHostVirtAddr() {
			return r.shiftedOffset(hostVirtAddr), r.PageSize, nil
		}
	}

	return 0, 0, AddressNotFoundError{hostVirtAddr: hostVirtAddr}
}

// GetHostVirtAddr returns the host virtual address and size of the remaining contiguous mapped host range for the given offset.
func (m *Mapping) GetHostVirtAddr(off int64) (uintptr, int64, error) {
	for _, r := range m.Regions {
		if off >= int64(r.Offset) && off < r.endOffset() {
			return r.shiftedHostVirtAddr(off), r.endOffset() - off, nil
		}
	}

	return 0, 0, OffsetNotFoundError{offset: off}
}

func (m *Mapping) PageSize() int64 {
	return m.pageSize
}

func (m *Mapping) TotalPages() (pages int64) {
	return header.TotalBlocks(m.TotalSize(), m.pageSize)
}

func (m *Mapping) TotalSize() (size int64) {
	for _, r := range m.Regions {
		size += int64(r.Size)
	}

	return size
}

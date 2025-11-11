package memory

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
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
func (m *Mapping) GetHostVirtRanges(off int64, size int64) (hostVirtRanges []block.Range, err error) {
	for n := int64(0); n < size; {
		currentOff := off + n

		region, err := m.getHostVirtRegion(currentOff)
		if err != nil {
			return nil, err
		}

		start := region.shiftedHostVirtAddr(currentOff)
		remainingSize := min(int64(region.endHostVirtAddr()-start), size-n)

		r := block.NewRange(int64(start), remainingSize)

		hostVirtRanges = append(hostVirtRanges, r)

		n += r.Size
	}

	return hostVirtRanges, nil
}

// getHostVirtRegion returns the region that contains the given offset.
func (m *Mapping) getHostVirtRegion(off int64) (*Region, error) {
	for _, r := range m.Regions {
		if off >= int64(r.Offset) && off < r.endOffset() {
			return &r, nil
		}
	}

	return nil, OffsetNotFoundError{offset: off}
}

package memory

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
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
	r, err := m.getHostVirtMapping(off)
	if err != nil {
		return 0, 0, err
	}

	return int64(r.shiftedHostVirtAddr(off)), uint64(r.PageSize), nil
}

// GetHostVirtRanges returns the host virtual addresses and sizes (ranges) that cover exactly the given [offset, offset+length) range in the host virtual address space.
func (m *Mapping) GetHostVirtRanges(off int64, size int64) (hostVirtRanges []block.Range, err error) {
	for n := int64(0); n < size; {
		currentOff := off + n

		region, err := m.getHostVirtMapping(currentOff)
		if err != nil {
			return nil, fmt.Errorf("failed to get host virt mapping: %w", err)
		}

		start := region.shiftedHostVirtAddr(currentOff)
		s := min(int64(region.endHostVirtAddr()-start), size-n)

		r := block.NewRange(int64(start), s)

		hostVirtRanges = append(hostVirtRanges, r)

		n += r.Size
	}

	return hostVirtRanges, nil
}

func (m *Mapping) getHostVirtMapping(off int64) (*Region, error) {
	for _, r := range m.Regions {
		if off >= int64(r.Offset) && off < r.endOffset() {
			return &r, nil
		}
	}

	return nil, fmt.Errorf("offset %d not found in any mapping", off)
}

package memory

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

type Mapping struct {
	regions []Region
}

func NewMapping(regions []Region) *Mapping {
	return &Mapping{regions: regions}
}

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

// GetOffset returns the relative offset and the page size of the mapped range for a given address.
func (m *Mapping) GetOffset(hostVirtAddr uintptr) (int64, uint64, error) {
	for _, r := range m.regions {
		if hostVirtAddr >= r.BaseHostVirtAddr && hostVirtAddr < r.BaseHostVirtAddr+r.Size {
			return int64(r.Offset + hostVirtAddr - r.BaseHostVirtAddr), uint64(r.PageSize), nil
		}
	}

	return 0, 0, fmt.Errorf("address %d not found in any mapping", hostVirtAddr)
}

// GetHostVirtAddr returns the host virtual address for a given offset.
func (m *Mapping) GetHostVirtAddr(offset int64) (int64, uint64, error) {
	r, err := m.getHostVirtMapping(offset)
	if err != nil {
		return 0, 0, err
	}

	return int64(r.BaseHostVirtAddr), uint64(r.PageSize), nil
}

// GetHostVirtRanges returns the host virtual addresses with size (ranges) that cover the given offset to offset+length.
func (m *Mapping) GetHostVirtRanges(offset int64, length int64) (hostVirtRanges []block.Range, err error) {
	for n := int64(0); n < length; {
		currentOffset := offset + n

		mapping, err := m.getHostVirtMapping(currentOffset)
		if err != nil {
			return nil, fmt.Errorf("failed to get host virt mapping: %w", err)
		}

		addr := int64(mapping.BaseHostVirtAddr) + currentOffset - int64(mapping.Offset)

		if addr < 0 {
			return nil, fmt.Errorf("address %d is less than 0, which is not possible", addr)
		}

		size := min(int64(mapping.Size)-currentOffset+int64(mapping.Offset), length-n)

		if size < 0 {
			return nil, fmt.Errorf("size %d is less than 0, which is not possible. offset: %d, length: %d, n: %d", size, offset, length, n)
		}

		r := block.NewRange(addr, size)

		hostVirtRanges = append(hostVirtRanges, r)

		n += int64(r.Size)
	}

	return hostVirtRanges, nil
}

func (m *Mapping) Regions() []Region {
	return m.regions
}

func (m *Mapping) getHostVirtMapping(offset int64) (*Region, error) {
	for _, r := range m.regions {
		if offset >= int64(r.Offset) && offset < int64(r.Offset+r.Size) {
			return &r, nil
		}
	}

	return nil, fmt.Errorf("offset %d not found in any mapping", offset)
}

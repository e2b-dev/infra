package memory

import (
	"encoding/binary"
	"fmt"
	"os"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	pageShift  = 12       // 2^12 = 4096 bytes
	entrySize  = int64(8) // 8-byte pagemap entry
	presentBit = 63       // PRESENT bit: 1 = page is in physical memory, 0 = never faulted/accessed
	// Note: We use PRESENT bit (not SOFT_DIRTY) to detect pages that were never read/accessed.
	// For hugepages, non-resident (PRESENT=0) means the page was never read/accessed.
	// SOFT_DIRTY bit (55) tracks writes after clearing and requires clearing first, so it's not suitable
	// for detecting pages that were never accessed at all.
)

func ResidentPages(pid int, m *Mapping) (*bitset.BitSet, error) {
	pm, err := os.Open(fmt.Sprintf("/proc/%d/pagemap", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open pagemap: %w", err)
	}
	defer pm.Close()

	resident := bitset.New(0)
	buf := make([]byte, entrySize)

	for _, r := range m.Regions {
		for offset := range r.Offsets() {
			hostVirtAddr, _, err := m.GetHostVirtAddr(offset)
			if err != nil {
				return nil, fmt.Errorf("failed to get host virt addr for offset %d: %w", offset, err)
			}

			pmPageIndex := uintptr(hostVirtAddr) >> pageShift
			pmOffset := int64(pmPageIndex) * entrySize

			if _, err := pm.ReadAt(buf, pmOffset); err != nil {
				return nil, fmt.Errorf("failed to read pagemap entry at offset %d: %w", pmOffset, err)
			}

			entry := binary.LittleEndian.Uint64(buf)

			present := (entry >> presentBit) & 1
			if present == 1 {
				resident.Set(uint(header.BlockIdx(offset, int64(r.PageSize))))
			}
		}
	}

	return resident, nil
}

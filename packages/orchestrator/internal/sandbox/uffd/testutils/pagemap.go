package testutils

import (
	"encoding/binary"
	"fmt"
	"os"
)

// Pagemap always uses the host page size (4K) for indexing,
// regardless of hugepages.
const (
	pagemapEntrySize = 8
	hostPageSize     = 4096

	// https://docs.kernel.org/admin-guide/mm/pagemap.html
	pmUffdWP  = uint64(1) << 57 // Page is write-protected via userfaultfd
	pmPresent = uint64(1) << 63 // Page is present in RAM
)

// PagemapEntry represents a single 8-byte entry from /proc/self/pagemap.
//
// Bit layout:
//   - Bits 0-54:  PFN if page present, 0-4: page type 5-54: swap offset if page swapped
//   - Bit 55:     Soft-dirty
//   - Bit 56:     Exclusively mapped
//   - Bit 57:     Write-protected via userfaultfd
//   - Bit 58-60:  Zero
//   - Bit 61:     Page is file-page or shared-anon
//   - Bit 62:     Page is swapped
//   - Bit 63:     Present in RAM
type PagemapEntry struct {
	Raw uint64
}

func (e PagemapEntry) IsPresent() bool {
	return e.Raw&pmPresent != 0
}

// IsWriteProtected returns true if the uffd WP bit (bit 57) is set.
func (e PagemapEntry) IsWriteProtected() bool {
	return e.Raw&pmUffdWP != 0
}

// PagemapReader reads entries from /proc/self/pagemap using pread.
// Modeled after the Firecracker pagemap reader (src/vmm/src/utils/pagemap.rs).
type PagemapReader struct {
	f *os.File
}

func NewPagemapReader() (*PagemapReader, error) {
	f, err := os.Open("/proc/self/pagemap")
	if err != nil {
		return nil, fmt.Errorf("open /proc/self/pagemap: %w", err)
	}

	return &PagemapReader{f: f}, nil
}

func (r *PagemapReader) Close() error {
	return r.f.Close()
}

// ReadEntry reads the pagemap entry for the host page containing virtAddr.
// For hugepages, this samples the first host page of the hugepage,
// which is sufficient since all host pages within a hugepage share the same WP state.
func (r *PagemapReader) ReadEntry(virtAddr uintptr) (PagemapEntry, error) {
	vpn := uint64(virtAddr) / hostPageSize
	offset := int64(vpn * pagemapEntrySize)

	var buf [pagemapEntrySize]byte

	n, err := r.f.ReadAt(buf[:], offset)
	if err != nil {
		return PagemapEntry{}, fmt.Errorf("read pagemap at vaddr %#x (file offset %d): %w", virtAddr, offset, err)
	}

	if n != pagemapEntrySize {
		return PagemapEntry{}, fmt.Errorf("short pagemap read: got %d bytes, want %d", n, pagemapEntrySize)
	}

	return PagemapEntry{Raw: binary.NativeEndian.Uint64(buf[:])}, nil
}

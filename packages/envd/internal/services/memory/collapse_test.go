//go:build linux

package memory

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestParseAnonRWRegions(t *testing.T) {
	t.Parallel()

	// A representative /proc/<pid>/maps excerpt: two anonymous rw regions (Go
	// heap arenas), plus a file-backed mapping, a [stack] pseudo-mapping, a
	// read-only anon mapping, and a malformed line — all of which must be
	// excluded.
	maps := strings.Join([]string{
		"014a00000000-014a00200000 rw-p 00000000 00:00 0 ",
		"7f0000000000-7f0000001000 rw-p 00000000 08:01 1234   /usr/lib/x.so",
		"7ffd00000000-7ffd00021000 rw-p 00000000 00:00 0   [stack]",
		"560000000000-560000001000 r--p 00000000 00:00 0 ",
		"this is a malformed line",
		"014b00000000-014b00400000 rw-p 00000000 00:00 0 ",
	}, "\n")

	regions, err := parseAnonRWRegions(strings.NewReader(maps))
	require.NoError(t, err)
	require.Len(t, regions, 2)

	assert.Equal(t, uintptr(0x014a00000000), regions[0].start)
	assert.Equal(t, uintptr(0x014a00200000), regions[0].end)
	assert.Equal(t, uintptr(0x014b00000000), regions[1].start)
	assert.Equal(t, uintptr(0x014b00400000), regions[1].end)
}

func TestParseAnonRWRegionsEmpty(t *testing.T) {
	t.Parallel()

	regions, err := parseAnonRWRegions(strings.NewReader(""))
	require.NoError(t, err)
	assert.Empty(t, regions)
}

// TestCollapseRange maps a sparsely-populated anonymous region (one touched
// page per 2 MiB window) and verifies collapseRange consolidates every window.
func TestCollapseRange(t *testing.T) {
	t.Parallel()

	const windows = 8
	const size = windows * hugePageSize

	// Over-allocate by one hugepage so we can 2 MiB-align the start.
	raw, err := unix.Mmap(-1, 0, size+hugePageSize,
		unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	require.NoError(t, err)
	defer func() { _ = unix.Munmap(raw) }()

	base := uintptr(unsafe.Pointer(&raw[0]))
	aligned := (base + hugePageSize - 1) &^ uintptr(hugePageSize-1)
	off := int(aligned - base)

	// Sparsely touch one byte per 2 MiB window so each window has live pages to
	// migrate but is far from fully present (the scattered-heap shape).
	for i := range windows {
		raw[off+i*hugePageSize] = 1
	}

	s := collapseRange(aligned, aligned+uintptr(size))
	assert.Equal(t, windows, s.Chunks)
	if s.Collapsed == 0 {
		t.Skip("MADV_COLLAPSE unsupported on this kernel; only window count verified")
	}
	assert.Equal(t, windows, s.Collapsed)
	assert.Zero(t, s.Skipped)
}

// TestCollapseSelf is a smoke test that collapsing the test process's own heap
// scans at least one anonymous region and returns no error.
func TestCollapseSelf(t *testing.T) {
	t.Parallel()

	s, err := CollapseSelf()
	require.NoError(t, err)
	assert.Positive(t, s.Regions)
	assert.Equal(t, s.Chunks, s.Collapsed+s.Skipped)
}

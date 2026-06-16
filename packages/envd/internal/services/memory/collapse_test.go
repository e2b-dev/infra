//go:build linux

package memory

import (
	"context"
	"testing"
	"unsafe"

	"github.com/prometheus/procfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestAnonRWRegions(t *testing.T) {
	t.Parallel()

	rw := func() *procfs.ProcMapPermissions {
		return &procfs.ProcMapPermissions{Read: true, Write: true}
	}

	// A representative parsed /proc/<pid>/maps: two anonymous rw regions (Go heap
	// arenas), plus a file-backed mapping, a [stack] pseudo-mapping, and a
	// read-only anon mapping — all of which must be excluded.
	maps := []*procfs.ProcMap{
		{StartAddr: 0x014a00000000, EndAddr: 0x014a00200000, Perms: rw()},
		{StartAddr: 0x7f0000000000, EndAddr: 0x7f0000001000, Perms: rw(), Pathname: "/usr/lib/x.so"},
		{StartAddr: 0x7ffd00000000, EndAddr: 0x7ffd00021000, Perms: rw(), Pathname: "[stack]"},
		{StartAddr: 0x560000000000, EndAddr: 0x560000001000, Perms: &procfs.ProcMapPermissions{Read: true}},
		{StartAddr: 0x014b00000000, EndAddr: 0x014b00400000, Perms: rw()},
	}

	regions := anonRWRegions(maps)
	require.Len(t, regions, 2)

	assert.Equal(t, uintptr(0x014a00000000), regions[0].start)
	assert.Equal(t, uintptr(0x014a00200000), regions[0].end)
	assert.Equal(t, uintptr(0x014b00000000), regions[1].start)
	assert.Equal(t, uintptr(0x014b00400000), regions[1].end)
}

func TestAnonRWRegionsEmpty(t *testing.T) {
	t.Parallel()

	assert.Empty(t, anonRWRegions(nil))
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

	s := collapseRange(context.Background(), aligned, aligned+uintptr(size))
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

	s, err := CollapseSelf(context.Background())
	require.NoError(t, err)
	assert.Positive(t, s.Regions)
	assert.Equal(t, s.Chunks, s.Collapsed+s.Skipped)
}

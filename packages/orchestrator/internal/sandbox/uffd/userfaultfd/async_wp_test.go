package userfaultfd

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestAsyncWriteProtection tests the UFFD_FEATURE_WP_ASYNC kernel feature.
//
// With WP_ASYNC the kernel handles write faults on write-protected pages
// automatically (clearing the WP bit and allowing the write) without
// sending a notification to the uffd handler. Dirty state is then read
// from /proc/self/pagemap by checking the uffd WP bit (bit 57):
//   - present + WP set   → page is clean (only read)
//   - present + WP clear → page is dirty (was written to)
//
// Operations are executed in exact sequence so we can verify that
// specific orderings (read→write, write→read, interleaved, etc.)
// produce the correct dirty/clean state.
//
// This mirrors the approach used in the e2b Firecracker fork
// (src/vmm/src/utils/pagemap.rs) but skips the mincore check.
func TestAsyncWriteProtection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		pagesize      uint64
		numberOfPages uint64
		operations    []operation
		expectedDirty []int
		expectedClean []int
		alwaysWP      bool
	}{
		{
			name:          "4k read then write same page",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: 0, mode: operationModeWrite},
			},
			expectedDirty: []int{0},
		},
		{
			name:          "4k write then read same page",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0, mode: operationModeWrite},
				{offset: 0, mode: operationModeRead},
			},
			expectedDirty: []int{0},
		},
		{
			name:          "4k write to missing page (no prior read)",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.PageSize, mode: operationModeWrite},
				{offset: 2 * header.PageSize, mode: operationModeWrite},
			},
			expectedDirty: []int{0, 2},
		},
		{
			name:          "4k all pages clean after read-only",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.PageSize, mode: operationModeRead},
				{offset: 1 * header.PageSize, mode: operationModeRead},
				{offset: 2 * header.PageSize, mode: operationModeRead},
				{offset: 3 * header.PageSize, mode: operationModeRead},
			},
			expectedClean: []int{0, 1, 2, 3},
		},
		{
			name:          "4k interleaved across pages",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.PageSize, mode: operationModeRead},
				{offset: 1 * header.PageSize, mode: operationModeWrite},
				{offset: 1 * header.PageSize, mode: operationModeRead},
				{offset: 0 * header.PageSize, mode: operationModeWrite},
			},
			expectedDirty: []int{0, 1},
		},
		{
			name:          "4k read-write-read stays dirty",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: 0, mode: operationModeWrite},
				{offset: 0, mode: operationModeRead},
			},
			expectedDirty: []int{0},
		},
		{
			name:          "4k write all then read all",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.PageSize, mode: operationModeWrite},
				{offset: 1 * header.PageSize, mode: operationModeWrite},
				{offset: 0 * header.PageSize, mode: operationModeRead},
				{offset: 1 * header.PageSize, mode: operationModeRead},
			},
			expectedDirty: []int{0, 1},
		},
		{
			name:          "4k selective write among reads",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.PageSize, mode: operationModeRead},
				{offset: 1 * header.PageSize, mode: operationModeRead},
				{offset: 2 * header.PageSize, mode: operationModeRead},
				{offset: 1 * header.PageSize, mode: operationModeWrite},
			},
			expectedDirty: []int{1},
			expectedClean: []int{0, 2},
		},
		{
			name:          "4k alternating read-write across pages",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.PageSize, mode: operationModeRead},
				{offset: 0 * header.PageSize, mode: operationModeWrite},
				{offset: 1 * header.PageSize, mode: operationModeRead},
				{offset: 2 * header.PageSize, mode: operationModeRead},
				{offset: 2 * header.PageSize, mode: operationModeWrite},
				{offset: 3 * header.PageSize, mode: operationModeRead},
			},
			expectedDirty: []int{0, 2},
			expectedClean: []int{1, 3},
		},
		{
			name:          "hugepage read then write same page",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: 0, mode: operationModeWrite},
			},
			expectedDirty: []int{0},
		},
		{
			name:          "hugepage write then read same page",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0, mode: operationModeWrite},
				{offset: 0, mode: operationModeRead},
			},
			expectedDirty: []int{0},
		},
		{
			name:          "hugepage write to missing page (no prior read)",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.HugepageSize, mode: operationModeWrite},
				{offset: 2 * header.HugepageSize, mode: operationModeWrite},
			},
			expectedDirty: []int{0, 2},
		},
		{
			name:          "hugepage all pages clean after read-only",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.HugepageSize, mode: operationModeRead},
				{offset: 1 * header.HugepageSize, mode: operationModeRead},
				{offset: 2 * header.HugepageSize, mode: operationModeRead},
				{offset: 3 * header.HugepageSize, mode: operationModeRead},
			},
			expectedClean: []int{0, 1, 2, 3},
		},
		{
			name:          "hugepage interleaved across pages",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.HugepageSize, mode: operationModeRead},
				{offset: 1 * header.HugepageSize, mode: operationModeWrite},
				{offset: 1 * header.HugepageSize, mode: operationModeRead},
				{offset: 0 * header.HugepageSize, mode: operationModeWrite},
			},
			expectedDirty: []int{0, 1},
		},
		{
			name:          "hugepage selective write among reads",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.HugepageSize, mode: operationModeRead},
				{offset: 1 * header.HugepageSize, mode: operationModeRead},
				{offset: 2 * header.HugepageSize, mode: operationModeRead},
				{offset: 1 * header.HugepageSize, mode: operationModeWrite},
			},
			expectedDirty: []int{1},
			expectedClean: []int{0, 2},
		},
		{
			name:          "hugepage alternating read-write across pages",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			operations: []operation{
				{offset: 0 * header.HugepageSize, mode: operationModeRead},
				{offset: 0 * header.HugepageSize, mode: operationModeWrite},
				{offset: 1 * header.HugepageSize, mode: operationModeRead},
				{offset: 2 * header.HugepageSize, mode: operationModeRead},
				{offset: 2 * header.HugepageSize, mode: operationModeWrite},
				{offset: 3 * header.HugepageSize, mode: operationModeRead},
			},
			expectedDirty: []int{0, 2},
			expectedClean: []int{1, 3},
		},

		// alwaysWP tests: handler copies with UFFDIO_COPY_MODE_WP for all faults,
		// including writes. WP_ASYNC must automatically clear the WP bit when the
		// original access was a write. This validates the assumption that independent
		// prefaulting (always copy with WP) works correctly.
		{
			name:          "4k alwaysWP write to missing page",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			alwaysWP:      true,
			operations: []operation{
				{offset: 0, mode: operationModeWrite},
			},
			expectedDirty: []int{0},
		},
		{
			name:          "4k alwaysWP mixed writes and reads",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			alwaysWP:      true,
			operations: []operation{
				{offset: 0 * header.PageSize, mode: operationModeWrite},
				{offset: 1 * header.PageSize, mode: operationModeRead},
				{offset: 2 * header.PageSize, mode: operationModeWrite},
				{offset: 3 * header.PageSize, mode: operationModeRead},
			},
			expectedDirty: []int{0, 2},
			expectedClean: []int{1, 3},
		},
		{
			name:          "hugepage alwaysWP write to missing page",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			alwaysWP:      true,
			operations: []operation{
				{offset: 0, mode: operationModeWrite},
			},
			expectedDirty: []int{0},
		},
		{
			name:          "hugepage alwaysWP mixed writes and reads",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			alwaysWP:      true,
			operations: []operation{
				{offset: 0 * header.HugepageSize, mode: operationModeWrite},
				{offset: 1 * header.HugepageSize, mode: operationModeRead},
				{offset: 2 * header.HugepageSize, mode: operationModeWrite},
				{offset: 3 * header.HugepageSize, mode: operationModeRead},
			},
			expectedDirty: []int{0, 2},
			expectedClean: []int{1, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t, testConfig{
				pagesize:      tt.pagesize,
				numberOfPages: tt.numberOfPages,
				alwaysWP:      tt.alwaysWP,
			})
			require.NoError(t, err)

			for i, op := range tt.operations {
				switch op.mode {
				case operationModeRead:
					err := h.executeRead(t.Context(), op)
					require.NoError(t, err, "step %d: read at offset %d", i, op.offset)
				case operationModeWrite:
					err := h.executeWrite(t.Context(), op)
					require.NoError(t, err, "step %d: write at offset %d", i, op.offset)
				}
			}

			pagemap, err := testutils.NewPagemapReader()
			require.NoError(t, err)
			defer pagemap.Close()

			memStart := uintptr(unsafe.Pointer(&(*h.memoryArea)[0]))

			for _, p := range tt.expectedDirty {
				addr := memStart + uintptr(p)*uintptr(tt.pagesize)
				entry, err := pagemap.ReadEntry(addr)
				require.NoError(t, err, "pagemap read for dirty page %d", p)

				assert.True(t, entry.IsPresent(), "dirty page %d should be present", p)
				assert.False(t, entry.IsWriteProtected(), "dirty page %d should have WP cleared", p)
			}

			for _, p := range tt.expectedClean {
				addr := memStart + uintptr(p)*uintptr(tt.pagesize)
				entry, err := pagemap.ReadEntry(addr)
				require.NoError(t, err, "pagemap read for clean page %d", p)

				assert.True(t, entry.IsPresent(), "clean page %d should be present", p)
				assert.True(t, entry.IsWriteProtected(), "clean page %d should have WP set", p)
			}
		})
	}
}

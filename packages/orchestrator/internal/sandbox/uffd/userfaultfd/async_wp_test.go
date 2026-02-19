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
// This mirrors the approach used in the e2b Firecracker fork
// (src/vmm/src/utils/pagemap.rs) but skips the mincore check.
func TestAsyncWriteProtection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		pagesize      uint64
		numberOfPages uint64
		// Pages to read first (indices).
		readPages []int
		// Pages to write after reading (indices). May include pages not in readPages (write-to-missing).
		writePages []int
		// Expected dirty pages: present and WP cleared.
		expectedDirty []int
		// Expected clean pages: present and WP set.
		expectedClean []int
	}{
		{
			name:          "4k read then write clears WP",
			pagesize:      header.PageSize,
			numberOfPages: 8,
			readPages:     []int{0, 1, 2, 3},
			writePages:    []int{1, 3},
			expectedDirty: []int{1, 3},
			expectedClean: []int{0, 2},
		},
		{
			name:          "4k write to missing page has no WP",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			readPages:     []int{},
			writePages:    []int{0, 2},
			expectedDirty: []int{0, 2},
			expectedClean: []int{},
		},
		{
			name:          "4k all pages clean after read-only",
			pagesize:      header.PageSize,
			numberOfPages: 4,
			readPages:     []int{0, 1, 2, 3},
			writePages:    []int{},
			expectedDirty: []int{},
			expectedClean: []int{0, 1, 2, 3},
		},
		{
			name:          "hugepage read then write clears WP",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			readPages:     []int{0, 1, 2, 3},
			writePages:    []int{1, 3},
			expectedDirty: []int{1, 3},
			expectedClean: []int{0, 2},
		},
		{
			name:          "hugepage write to missing page has no WP",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			readPages:     []int{},
			writePages:    []int{0, 2},
			expectedDirty: []int{0, 2},
			expectedClean: []int{},
		},
		{
			name:          "hugepage all pages clean after read-only",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			readPages:     []int{0, 1, 2, 3},
			writePages:    []int{},
			expectedDirty: []int{},
			expectedClean: []int{0, 1, 2, 3},
		},
		{
			name:          "hugepage mix of read, write, and missing write",
			pagesize:      header.HugepageSize,
			numberOfPages: 4,
			readPages:     []int{0, 1},
			writePages:    []int{1, 2},
			expectedDirty: []int{1, 2},
			expectedClean: []int{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t, testConfig{
				pagesize:      tt.pagesize,
				numberOfPages: tt.numberOfPages,
			})
			require.NoError(t, err)

			for _, p := range tt.readPages {
				err := h.executeRead(t.Context(), operation{
					offset: int64(p) * int64(tt.pagesize),
					mode:   operationModeRead,
				})
				require.NoError(t, err, "read page %d", p)
			}

			for _, p := range tt.writePages {
				err := h.executeWrite(t.Context(), operation{
					offset: int64(p) * int64(tt.pagesize),
					mode:   operationModeWrite,
				})
				require.NoError(t, err, "write page %d", p)
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

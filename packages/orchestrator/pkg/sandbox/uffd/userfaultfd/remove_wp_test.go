//go:build linux

package userfaultfd

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestRemoveHugepageDropsMissingKeepsWP exercises the hugepage REMOVE
// optimization end-to-end through the real serve loop: after MADV_DONTNEED the
// handler unregisters MISSING for the range (so the kernel serves the zero page
// directly, with no re-fault to the handler) while WP-async keeps tracking
// writes.
//
// A post-remove write is therefore NOT observed by the handler — the page stays
// in the removed/zero tracker state — yet it still shows up as dirty in the
// pagemap, which is what the snapshot diff relies on.
func TestRemoveHugepageDropsMissingKeepsWP(t *testing.T) {
	t.Parallel()

	tt := testConfig{
		name:          "hugepage remove then write keeps WP tracking",
		pagesize:      header.HugepageSize,
		numberOfPages: 2,
		removeEnabled: true,
		operations: []operation{
			{offset: 0, mode: operationModeRead},
			{offset: 0, mode: operationModeRemove},
			// Let the serve loop process the REMOVE (unregister + WP re-arm)
			// before the write, so the write is genuinely kernel-served.
			{mode: operationModeSleep},
			{offset: 0, mode: operationModeWrite},
		},
	}

	h, err := configureCrossProcessTest(t.Context(), t, tt)
	require.NoError(t, err)

	h.executeAll(t, tt.operations)

	states, err := h.pageStates()
	require.NoError(t, err)

	// MISSING was dropped on REMOVE, so the kernel served the post-remove write
	// without delivering a fault to the handler.
	assert.Contains(t, states.removed, uint(0),
		"removed hugepage should stay in the zero/removed tracker state (no re-fault)")
	assert.NotContains(t, states.faulted, uint(0),
		"handler must not observe a MISSING fault for the kernel-served write")

	// The write is still captured by WP-async: the page is present and its uffd
	// WP bit is cleared (dirty).
	pagemap, err := testutils.NewPagemapReader()
	require.NoError(t, err)
	defer pagemap.Close()

	memStart := uintptr(unsafe.Pointer(&(*h.memoryArea)[0]))
	entry, err := pagemap.ReadEntry(memStart)
	require.NoError(t, err)
	assert.True(t, entry.IsPresent(), "written hugepage should be present")
	assert.False(t, entry.IsWriteProtected(), "written hugepage should be dirty (WP cleared)")
}

package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestRemove(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "4k read then remove",
			pagesize:      header.PageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: 0, mode: operationModeRemove},
				{mode: operationModeSleep},
			},
		},
		{
			name:          "hugepage read then remove",
			pagesize:      header.HugepageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: 0, mode: operationModeRemove},
				{mode: operationModeSleep},
			},
		},
		{
			name:          "4k write then remove",
			pagesize:      header.PageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeWrite},
				{offset: 0, mode: operationModeRemove},
				{mode: operationModeSleep},
			},
		},
		{
			name:          "hugepage write then remove",
			pagesize:      header.HugepageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeWrite},
				{offset: 0, mode: operationModeRemove},
				{mode: operationModeSleep},
			},
		},
		{
			name:          "4k selective remove",
			pagesize:      header.PageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: int64(header.PageSize), mode: operationModeWrite},
				{offset: 0, mode: operationModeRemove},
				{mode: operationModeSleep},
			},
		},
		{
			name:          "hugepage selective remove",
			pagesize:      header.HugepageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: int64(header.HugepageSize), mode: operationModeWrite},
				{offset: 0, mode: operationModeRemove},
				{mode: operationModeSleep},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t.Context(), t, tt)
			require.NoError(t, err)

			h.executeAll(t, tt.operations)

			states, err := h.pageStates()
			require.NoError(t, err)

			removedOffsets := getOperationsOffsets(tt.operations, operationModeRemove)
			assert.ElementsMatch(t, removedOffsets, states.removed)

			faultedOffsets := getOperationsOffsets(tt.operations, operationModeRead|operationModeWrite)
			for _, r := range removedOffsets {
				faultedOffsets = removeOffset(faultedOffsets, r)
			}
			assert.ElementsMatch(t, faultedOffsets, states.faulted)

			h.checkDirtiness(t, tt.operations)
		})
	}
}

// TestRemoveThenFault asserts that after MADV_DONTNEED + a subsequent write,
// the handler re-faults the page (state transitions: faulted → removed → faulted).
func TestRemoveThenFault(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "4k read, remove, write",
			pagesize:      header.PageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: 0, mode: operationModeRemove},
				{offset: 0, mode: operationModeWrite},
			},
		},
		{
			name:          "hugepage read, remove, write",
			pagesize:      header.HugepageSize,
			numberOfPages: 2,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{offset: 0, mode: operationModeRemove},
				{offset: 0, mode: operationModeWrite},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t.Context(), t, tt)
			require.NoError(t, err)

			h.executeAll(t, tt.operations)

			states, err := h.pageStates()
			require.NoError(t, err)

			assert.Empty(t, states.removed, "page should not be in removed state after re-fault")
			assert.Contains(t, states.faulted, uint(0), "page should be back in faulted state")

			h.checkDirtiness(t, tt.operations)
		})
	}
}

// TestRemoveThenWriteGated verifies that when the handler is stopped, the
// kernel keeps the page mapped until REMOVE is acked. A concurrent write
// succeeds without faulting because MADV_DONTNEED blocks (waiting for ack)
// and doesn't unmap the page until the handler processes the event.
// When the handler resumes, it only sees the REMOVE — no MISSING fault.
func TestRemoveThenWriteGated(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "4k gated remove with concurrent write",
			pagesize:      header.PageSize,
			numberOfPages: 2,
			gated:         true,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{mode: operationModeServePause},
				{offset: 0, mode: operationModeRemove, async: true},
				{mode: operationModeSleep},
				{offset: 0, mode: operationModeWrite, async: true},
				{mode: operationModeSleep},
				{mode: operationModeServeResume},
			},
		},
		{
			name:          "hugepage gated remove with concurrent write",
			pagesize:      header.HugepageSize,
			numberOfPages: 2,
			gated:         true,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{mode: operationModeServePause},
				{offset: 0, mode: operationModeRemove, async: true},
				{mode: operationModeSleep},
				{offset: 0, mode: operationModeWrite, async: true},
				{mode: operationModeSleep},
				{mode: operationModeServeResume},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t.Context(), t, tt)
			require.NoError(t, err)

			h.executeAll(t, tt.operations)

			states, err := h.pageStates()
			require.NoError(t, err)

			// The page stays mapped until REMOVE is acked, so the concurrent
			// write succeeds without triggering a MISSING fault. The handler
			// only processes the REMOVE event.
			assert.ElementsMatch(t, []uint{0}, states.removed)
			assert.Empty(t, states.faulted)
		})
	}
}

// TestWriteThenRemoveGated verifies the serve loop's ordering guarantee:
// REMOVE events are processed before pagefaults even when the MISSING pagefault
// was queued first. The write to a missing page triggers MISSING (queued first),
// then MADV_DONTNEED triggers REMOVE (queued second). When the handler resumes,
// it processes REMOVE first, then MISSING — the write is not skipped.
func TestWriteThenRemoveGated(t *testing.T) {
	t.Parallel()

	tests := []testConfig{
		{
			name:          "4k write then remove in same batch",
			pagesize:      header.PageSize,
			numberOfPages: 2,
			gated:         true,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{mode: operationModeServePause},
				// MISSING for page 1 queued first
				{offset: int64(header.PageSize), mode: operationModeWrite, async: true},
				{mode: operationModeSleep},
				// REMOVE for page 0 queued second
				{offset: 0, mode: operationModeRemove, async: true},
				{mode: operationModeSleep},
				{mode: operationModeServeResume},
			},
		},
		{
			name:          "hugepage write then remove in same batch",
			pagesize:      header.HugepageSize,
			numberOfPages: 2,
			gated:         true,
			removeEnabled: true,
			operations: []operation{
				{offset: 0, mode: operationModeRead},
				{mode: operationModeServePause},
				{offset: int64(header.HugepageSize), mode: operationModeWrite, async: true},
				{mode: operationModeSleep},
				{offset: 0, mode: operationModeRemove, async: true},
				{mode: operationModeSleep},
				{mode: operationModeServeResume},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, err := configureCrossProcessTest(t.Context(), t, tt)
			require.NoError(t, err)

			h.executeAll(t, tt.operations)

			states, err := h.pageStates()
			require.NoError(t, err)

			// Page 0 was removed
			assert.Contains(t, states.removed, uint(0))
			// Page 1 was faulted by the write — not skipped
			pageOffset := uint(tt.pagesize)
			assert.Contains(t, states.faulted, pageOffset,
				"write pagefault should not be skipped even when batched with REMOVE")
		})
	}
}

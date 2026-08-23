//go:build linux

package uffd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// stateOnlyUffd wires a state-only handler (no fd) into a backend so the
// DiffMetadata surface can be exercised without a live userfaultfd. The
// fc.Process parameter is only touched by the pagemap branch, so these tests
// stay on the tracker-source and fail-closed paths.
func stateOnlyUffd(t *testing.T, tracker *block.Tracker, syncWP bool) *Uffd {
	t.Helper()

	u := New(nil, "")
	u.handler.SetValue(userfaultfd.NewStateOnly(tracker, uintptr(header.HugepageSize), logger.NewNopLogger()))
	u.SetSyncWP(syncWP)

	return u
}

// TestDiffMetadataTrackerSourceFailsClosed: requesting the tracker dirty
// source for a sandbox without sync-WP fault delivery must be a loud pause
// failure — under WP_ASYNC the tracker misses every post-install write, so
// honoring the request would silently corrupt the snapshot.
func TestDiffMetadataTrackerSourceFailsClosed(t *testing.T) {
	t.Parallel()

	u := stateOnlyUffd(t, block.NewTracker(), false /* syncWP */)

	_, err := u.DiffMetadata(t.Context(), nil, true /* useTrackerDirty */)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "without sync-WP fault delivery")
}

// TestDiffMetadataTrackerSource: the tracker-sourced diff must map states
// exactly — Dirty exported as dirty, Zero and Removed merged into empty,
// Clean in NEITHER set (inherited from the parent layer), NotPresent absent.
func TestDiffMetadataTrackerSource(t *testing.T) {
	t.Parallel()

	tracker := block.NewTracker()
	tracker.SetRange(0, 2, block.Dirty)   // pages 0,1: guest-written
	tracker.SetRange(2, 3, block.Zero)    // page 2: installed zero
	tracker.SetRange(3, 4, block.Removed) // page 3: zapped by REMOVE
	tracker.SetRange(4, 5, block.Clean)   // page 4: read-installed, unwritten
	// page 5: never touched (NotPresent)

	u := stateOnlyUffd(t, tracker, true /* syncWP */)

	dm, err := u.DiffMetadata(t.Context(), nil, true /* useTrackerDirty */)
	require.NoError(t, err)

	assert.Equal(t, int64(header.HugepageSize), dm.BlockSize)
	assert.ElementsMatch(t, []uint32{0, 1}, dm.Dirty.ToArray(), "Dirty pages are the dirty set")
	assert.ElementsMatch(t, []uint32{2, 3}, dm.Empty.ToArray(), "Zero and Removed merge into the empty set")
	assert.False(t, dm.Dirty.Contains(4), "Clean must not be exported as dirty")
	assert.False(t, dm.Empty.Contains(4), "Clean must not be exported as empty: the diff inherits it from the parent")
	assert.False(t, dm.Dirty.Contains(5) || dm.Empty.Contains(5), "NotPresent appears in neither set")
}

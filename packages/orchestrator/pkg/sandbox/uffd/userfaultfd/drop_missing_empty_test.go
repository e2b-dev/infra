//go:build linux

package userfaultfd

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// faultTimeout bounds in-process page faults. A fault that never returns means
// MISSING tracking is still registered with no handler serving it — surface
// that as a clean failure instead of hanging the whole test binary.
const faultTimeout = 5 * time.Second

// runWithTimeout runs fn in a goroutine and fails the test if it does not
// return within faultTimeout.
func runWithTimeout(t *testing.T, what string, fn func() error) {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- fn() }()

	select {
	case err := <-done:
		require.NoError(t, err, what)
	case <-time.After(faultTimeout):
		t.Fatalf("%s timed out after %s: page fault was not served (MISSING tracking likely still registered)", what, faultTimeout)
	}
}

// newHandler builds a Userfaultfd over a freshly mmap'd, MISSING|WP registered
// region. It returns the handler, the mapped memory and its start address.
func newHandler(t *testing.T, pagesize uint64, numberOfPages uint64) (*Userfaultfd, []byte, uintptr) {
	t.Helper()

	size := pagesize * numberOfPages

	// Random source content: an empty page served from source (the regression
	// we're guarding against) would read as non-zero, so the zero-content
	// assertion below would catch it.
	src := RandomPages(pagesize, numberOfPages)

	memoryArea, memoryStart, err := testutils.NewPageMmap(t, size, pagesize)
	require.NoError(t, err)

	uffdFd, err := newFd(unix.O_CLOEXEC | unix.O_NONBLOCK)
	require.NoError(t, err)
	t.Cleanup(func() { uffdFd.close() })

	require.NoError(t, configureApi(uffdFd, pagesize, false))
	require.NoError(t, register(uffdFd, memoryStart, size, UFFDIO_REGISTER_MODE_MISSING|UFFDIO_REGISTER_MODE_WP))

	mapping := memory.NewMapping([]memory.Region{
		{
			BaseHostVirtAddr: memoryStart,
			Size:             uintptr(size),
			Offset:           0,
			PageSize:         uintptr(pagesize),
		},
	})

	log, err := logger.NewDevelopmentLogger()
	require.NoError(t, err)

	u, err := NewUserfaultfdFromFd(uintptr(uffdFd), src, mapping, log)
	require.NoError(t, err)

	return u, memoryArea, memoryStart
}

// buildHeader builds a header whose mapping marks emptyPages as uuid.Nil (holes)
// and every other page as backed by a real build. The pages are 0-indexed in
// units of pagesize.
func buildHeader(t *testing.T, pagesize uint64, numberOfPages uint64, emptyPages map[uint64]bool) *header.Header {
	t.Helper()

	buildID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	var maps []header.BuildMap
	var storage uint64
	for p := range numberOfPages {
		m := header.BuildMap{
			Offset: p * pagesize,
			Length: pagesize,
		}
		if emptyPages[p] {
			m.BuildId = uuid.Nil
		} else {
			m.BuildId = buildID
			m.BuildStorageOffset = storage
			storage += pagesize
		}
		maps = append(maps, m)
	}

	h, err := header.NewHeader(header.NewTemplateMetadata(buildID, pagesize, pagesize*numberOfPages), maps)
	require.NoError(t, err)

	return h
}

// TestDropMissingForEmptyRangesHugepage verifies that DropMissingForEmptyRanges:
//   - drops MISSING tracking for the snapshot's empty (uuid.Nil) hugepages, so
//     the kernel serves them as zero without a handler (no fault hang);
//   - keeps WP-async tracking armed: a read-only empty page stays clean
//     (present + WP set) while a written one turns dirty (present + WP clear);
//   - leaves non-empty pages untouched (still MISSING-registered, not present).
//
// It also implicitly validates that UFFDIO_WRITEPROTECT works on an unpopulated
// hugepage range on this kernel — if it doesn't, the method returns an error.
func TestDropMissingForEmptyRangesHugepage(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("this test requires root privileges (reads /proc/self/pagemap WP bit)")
	}

	const numberOfPages = 4
	pagesize := uint64(header.HugepageSize)

	// Pages 0 and 2 are holes; 1 and 3 are backed.
	emptyPages := map[uint64]bool{0: true, 2: true}
	h := buildHeader(t, pagesize, numberOfPages, emptyPages)

	u, memoryArea, memoryStart := newHandler(t, pagesize, numberOfPages)

	require.NoError(t, u.DropMissingForEmptyRanges(t.Context(), h))

	pagemap, err := testutils.NewPagemapReader()
	require.NoError(t, err)
	defer pagemap.Close()

	pageAddr := func(p uint64) uintptr { return memoryStart + uintptr(p*pagesize) }
	pageBytes := func(p uint64) []byte { return memoryArea[p*pagesize : (p+1)*pagesize] }

	// Empty page, read-only: served by the kernel as zero, stays clean.
	runWithTimeout(t, "read empty page 0", func() error {
		return unix.Madvise(pageBytes(0), unix.MADV_POPULATE_READ)
	})

	entry, err := pagemap.ReadEntry(pageAddr(0))
	require.NoError(t, err)
	assert.True(t, entry.IsPresent(), "read empty page should be present")
	assert.True(t, entry.IsWriteProtected(), "read-only empty page should stay clean (WP set)")
	assert.True(t, bytes.Equal(pageBytes(0), make([]byte, pagesize)),
		"empty page must be kernel-served zeros, not source content")

	// Empty page, written: WP-async clears the WP bit → dirty.
	runWithTimeout(t, "write empty page 2", func() error {
		return unix.Madvise(pageBytes(2), unix.MADV_POPULATE_WRITE)
	})

	entry, err = pagemap.ReadEntry(pageAddr(2))
	require.NoError(t, err)
	assert.True(t, entry.IsPresent(), "written empty page should be present")
	assert.False(t, entry.IsWriteProtected(), "written empty page should be dirty (WP cleared)")

	// Non-empty pages are left MISSING-registered, so they must not have been
	// faulted in by the call.
	for _, p := range []uint64{1, 3} {
		entry, err := pagemap.ReadEntry(pageAddr(p))
		require.NoError(t, err)
		assert.False(t, entry.IsPresent(), "non-empty page %d must be left untouched", p)
	}
}

// TestDropMissingForEmptyRanges4KNoop verifies the method is a no-op for 4K
// pages: dropping MISSING for anonymous 4K pages would zap the PTE and lose WP
// tracking, so those pages must stay MISSING-registered (the call returns
// without touching the kernel registration).
func TestDropMissingForEmptyRanges4KNoop(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("this test requires root privileges")
	}

	const numberOfPages = 4
	pagesize := uint64(header.PageSize)

	emptyPages := map[uint64]bool{0: true, 2: true}
	h := buildHeader(t, pagesize, numberOfPages, emptyPages)

	u, _, _ := newHandler(t, pagesize, numberOfPages)

	// 4K is the no-op path: the call must return cleanly without unregistering
	// anything. A positive "still MISSING-registered" check would require
	// faulting a page, which blocks forever with no handler, so we assert the
	// contract via the guard returning nil.
	require.NoError(t, u.DropMissingForEmptyRanges(t.Context(), h))
}

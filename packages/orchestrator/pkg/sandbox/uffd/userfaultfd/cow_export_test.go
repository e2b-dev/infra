//go:build linux

package userfaultfd

import (
	"errors"
	"io"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestPackedPageSink verifies the identity→packed translation matches the
// diff-artifact convention (dirty pages concatenated in ascending page
// order — header.CreateMapping's BuildStorageOffset packing).
func TestPackedPageSink(t *testing.T) {
	t.Parallel()

	const ps = int64(header.PageSize)
	pages := roaring.BitmapOf(2, 5, 9)
	dst := newMemSink(3 * ps)
	sink := NewPackedPageSink(pages, ps, dst)

	page := func(b byte) []byte {
		p := make([]byte, ps)
		for i := range p {
			p[i] = b
		}

		return p
	}

	// Write out of order; packed slots follow ascending page order.
	_, err := sink.WriteAt(page(0x99), 9*ps)
	require.NoError(t, err)
	_, err = sink.WriteAt(page(0x22), 2*ps)
	require.NoError(t, err)
	_, err = sink.WriteAt(page(0x55), 5*ps)
	require.NoError(t, err)

	assert.Equal(t, byte(0x22), dst.data[0], "page 2 → packed slot 0")
	assert.Equal(t, byte(0x55), dst.data[ps], "page 5 → packed slot 1")
	assert.Equal(t, byte(0x99), dst.data[2*ps], "page 9 → packed slot 2")

	_, err = sink.WriteAt(page(0), 3*ps)
	require.Error(t, err, "page outside the set must be rejected")
	_, err = sink.WriteAt(page(0)[:10], 2*ps)
	require.Error(t, err, "partial page must be rejected")
	_, err = sink.WriteAt(page(0), 2*ps+1)
	require.Error(t, err, "unaligned offset must be rejected")
}

// TestCoWWindowWithPackedSink drives a window through the packed sink: the
// artifact must be the pre-images concatenated in ascending page order.
// Runs once per copyPage branch — the ReadAt fallback (memSrc) and the
// zero-copy Slice fast path (sliceSrc) production sources take — so neither
// branch's offset arithmetic can regress unseen.
func TestCoWWindowWithPackedSink(t *testing.T) {
	t.Parallel()

	sources := []struct {
		name string
		wrap func(m memSrc) io.ReaderAt
	}{
		{name: "readat fallback", wrap: func(m memSrc) io.ReaderAt { return m }},
		{name: "slice fast path", wrap: func(m memSrc) io.ReaderAt { return sliceSrc{m} }},
	}

	for _, tc := range sources {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const nPages = 16
			dirty := []uint32{1, 4, 5, 11, 15}
			size := int64(nPages) * testCoWPageSize
			src := make(memSrc, size)
			for i := range src {
				src[i] = byte(i / int(testCoWPageSize)) // page idx as fill byte
			}

			pages := roaring.New()
			pages.AddMany(dirty)
			packed := newMemSink(int64(len(dirty)) * testCoWPageSize)
			w := NewCoWWindow(pages, testCoWPageSize, tc.wrap(src), NewPackedPageSink(pages, testCoWPageSize, packed), nil)

			require.NoError(t, w.Sweep(t.Context()))
			require.NoError(t, w.Wait(t.Context()))

			for slot, idx := range dirty {
				off := int64(slot) * testCoWPageSize
				assert.Equal(t, byte(idx), packed.data[off], "packed slot %d holds page %d", slot, idx)
			}
		})
	}
}

// TestCoalesceWPRuns covers the arm-range coalescing: pages contiguous in
// memfile space merge into one run only when their HOST addresses are also
// contiguous — a region boundary must split the run even if the page indices
// are adjacent.
func TestCoalesceWPRuns(t *testing.T) {
	t.Parallel()

	const ps = uintptr(header.PageSize)
	// Two regions covering memfile offsets [0, 4 pages) and [4 pages, 8
	// pages), mapped at DISJOINT host addresses (gap between them).
	ma := memory.NewMapping([]memory.Region{
		{BaseHostVirtAddr: 0x10000, Size: 4 * ps, Offset: 0, PageSize: ps},
		{BaseHostVirtAddr: 0x90000, Size: 4 * ps, Offset: 4 * ps, PageSize: ps},
	})

	t.Run("contiguous pages within one region coalesce", func(t *testing.T) {
		t.Parallel()
		pages := roaring.BitmapOf(0, 1, 2)
		runs, err := coalesceWPRuns(pages, ma, ps)
		require.NoError(t, err)
		assert.Equal(t, []wpRun{{start: 0x10000, length: 3 * ps}}, runs)
	})

	t.Run("gap in the page set splits the run", func(t *testing.T) {
		t.Parallel()
		pages := roaring.BitmapOf(0, 2, 3)
		runs, err := coalesceWPRuns(pages, ma, ps)
		require.NoError(t, err)
		assert.Equal(t, []wpRun{
			{start: 0x10000, length: ps},
			{start: 0x10000 + 2*ps, length: 2 * ps},
		}, runs)
	})

	t.Run("region boundary splits index-adjacent pages", func(t *testing.T) {
		t.Parallel()
		pages := roaring.BitmapOf(2, 3, 4, 5)
		runs, err := coalesceWPRuns(pages, ma, ps)
		require.NoError(t, err)
		assert.Equal(t, []wpRun{
			{start: 0x10000 + 2*ps, length: 2 * ps},
			{start: 0x90000, length: 2 * ps},
		}, runs)
	})

	t.Run("out-of-range page fails", func(t *testing.T) {
		t.Parallel()
		pages := roaring.BitmapOf(9)
		_, err := coalesceWPRuns(pages, ma, ps)
		require.Error(t, err)
	})

	t.Run("empty set arms nothing", func(t *testing.T) {
		t.Parallel()
		runs, err := coalesceWPRuns(roaring.New(), ma, ps)
		require.NoError(t, err)
		assert.Empty(t, runs)
	})
}

// TestCoWWindowCancelOverridesRacedCompletion pins the authority of a cancel
// that loses the done race: the final page's completion resolves done with
// SUCCESS first (first-setter-wins), and a REMOVE-tripwire cancel lands only
// afterwards — exactly the interleaving where the racing copy may have read
// post-punch zeros. Sweep and Wait must still report the cancel, or the
// runner would publish an artifact containing a zapped page's zeros as
// pause-time content.
func TestCoWWindowCancelOverridesRacedCompletion(t *testing.T) {
	t.Parallel()

	pages := roaring.New()
	pages.Add(0)
	src := make(memSrc, testCoWPageSize)
	dst := newMemSink(testCoWPageSize)

	w := NewCoWWindow(pages, testCoWPageSize, src, dst, nil)

	// The final (only) page completes: done resolves success.
	handled, err := w.EnsureCopied(t.Context(), 0)
	require.NoError(t, err)
	require.True(t, handled)

	// The tripwire's cancel arrives after the completion won the done race.
	w.Cancel(errors.New("REMOVE zapped uncaptured window pages"))

	require.ErrorIs(t, w.Sweep(t.Context()), ErrCoWWindowCanceled)
	require.ErrorIs(t, w.Wait(t.Context()), ErrCoWWindowCanceled)
}

//go:build linux

package userfaultfd

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const testCoWPageSize = int64(header.PageSize)

// memSrc is a mutable in-memory page source. Mutating a page is only legal
// after EnsureCopied for that page returned — the same contract the real
// system upholds (the writer stays fault-blocked until the pre-image copy is
// done), so the race detector validates the window's ordering for free.
type memSrc []byte

func (m memSrc) ReadAt(p []byte, off int64) (int, error) {
	return copy(p, m[off:]), nil
}

// sliceSrc is memSrc plus the Slice method the memfd view exposes, so windows
// over it take copyPage's zero-copy fast path — the branch every production
// checkpoint uses. Kept separate from memSrc on purpose: memSrc pins the
// ReadAt fallback, sliceSrc the fast path, and the suite must cover both.
type sliceSrc struct{ memSrc }

func (s sliceSrc) Slice(off, length int64) ([]byte, error) {
	return s.memSrc[off : off+length], nil
}

// memSink records every page write; failAt injects a sink failure.
type memSink struct {
	mu     sync.Mutex
	data   []byte
	writes map[int64]int
	failAt int64 // byte offset that fails; -1 = never
}

func newMemSink(size int64) *memSink {
	return &memSink{data: make([]byte, size), writes: make(map[int64]int), failAt: -1}
}

func (s *memSink) WriteAt(p []byte, off int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt >= 0 && off == s.failAt {
		return 0, errors.New("injected sink failure")
	}
	s.writes[off]++

	return copy(s.data[off:], p), nil
}

func (s *memSink) writeCount(off int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.writes[off]
}

// newTestWindow builds a window over pages [0, n) ∩ dirty with random source
// content. Returns the window, the source, the sink and the original bytes.
func newTestWindow(t *testing.T, nPages int, dirty []uint32, tracker *block.Tracker) (*CoWWindow, memSrc, *memSink, []byte) {
	t.Helper()

	size := int64(nPages) * testCoWPageSize
	src := make(memSrc, size)
	_, err := rand.New(rand.NewSource(42)).Read(src)
	require.NoError(t, err)
	original := bytes.Clone(src)

	pages := roaring.New()
	pages.AddMany(dirty)

	sink := newMemSink(size)
	w := NewCoWWindow(pages, testCoWPageSize, src, sink, tracker)

	return w, src, sink, original
}

func pageRange(n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = uint32(i)
	}

	return out
}

func TestCoWWindowSweepCopiesAll(t *testing.T) {
	t.Parallel()

	dirty := []uint32{0, 3, 4, 7, 31}
	w, _, sink, original := newTestWindow(t, 32, dirty, nil)

	require.NoError(t, w.Sweep(t.Context()))
	require.NoError(t, w.Wait(t.Context()))

	for _, idx := range dirty {
		off := int64(idx) * testCoWPageSize
		assert.Equal(t, original[off:off+testCoWPageSize], sink.data[off:off+testCoWPageSize],
			"page %d content", idx)
		assert.Equal(t, 1, sink.writeCount(off), "page %d written exactly once", idx)
	}
	// Pages outside the window must not be touched.
	off := int64(1) * testCoWPageSize
	assert.Equal(t, 0, sink.writeCount(off), "page 1 is outside the window")
	assert.EqualValues(t, len(dirty), w.Copied())
}

// TestCoWWindowPreImageSentinel is the core correctness property: writers
// mutate pages concurrently with the sweep, each after its EnsureCopied
// returned (the fault-path contract — the guest writer is blocked until
// then). The sink must hold the ORIGINAL content for every page.
func TestCoWWindowPreImageSentinel(t *testing.T) {
	t.Parallel()

	const nPages = 256
	dirty := pageRange(nPages)
	w, src, sink, original := newTestWindow(t, nPages, dirty, nil)

	var eg errgroup.Group
	// Sweep runs concurrently with the writers.
	eg.Go(func() error { return w.Sweep(t.Context()) })

	for i := range nPages {
		eg.Go(func() error {
			idx := uint32(i)
			if _, err := w.EnsureCopied(t.Context(), idx); err != nil {
				return err
			}
			// The pre-image is captured; overwrite the page like the guest
			// write proceeding after unprotect+wake.
			off := int64(idx) * testCoWPageSize
			for b := off; b < off+testCoWPageSize; b++ {
				src[b] = 0xEE
			}

			return nil
		})
	}
	require.NoError(t, eg.Wait())
	require.NoError(t, w.Wait(t.Context()))

	assert.Equal(t, original, sink.data, "sink must hold pre-write content for every page")
}

// TestCoWWindowSliceFastPathPreImageSentinel is the sentinel property through
// copyPage's zero-copy Slice branch — the one every production checkpoint
// takes (the memfd view exposes Slice; only test sources don't). The source
// slice is LIVE memory that writers mutate the moment their EnsureCopied
// returns, so the race detector also checks the fast path's direct
// slice-to-sink write against the mutation, exactly the fault-path timing.
func TestCoWWindowSliceFastPathPreImageSentinel(t *testing.T) {
	t.Parallel()

	const nPages = 256
	size := int64(nPages) * testCoWPageSize
	src := sliceSrc{make(memSrc, size)}
	_, err := rand.New(rand.NewSource(43)).Read(src.memSrc)
	require.NoError(t, err)
	original := bytes.Clone(src.memSrc)

	pages := roaring.New()
	pages.AddMany(pageRange(nPages))
	sink := newMemSink(size)
	w := NewCoWWindow(pages, testCoWPageSize, src, sink, nil)

	var eg errgroup.Group
	eg.Go(func() error { return w.Sweep(t.Context()) })
	for i := range nPages {
		eg.Go(func() error {
			idx := uint32(i)
			if _, err := w.EnsureCopied(t.Context(), idx); err != nil {
				return err
			}
			off := int64(idx) * testCoWPageSize
			for b := off; b < off+testCoWPageSize; b++ {
				src.memSrc[b] = 0xEE
			}

			return nil
		})
	}
	require.NoError(t, eg.Wait())
	require.NoError(t, w.Wait(t.Context()))

	assert.Equal(t, original, sink.data, "fast path must capture pre-write content for every page")
	assert.EqualValues(t, nPages, w.Copied())
}

// TestCoWWindowCopyExactlyOnce hammers the same pages from many goroutines
// while the sweep runs: each page's pre-image must reach the sink exactly
// once (claim losers wait instead of double-copying).
func TestCoWWindowCopyExactlyOnce(t *testing.T) {
	t.Parallel()

	const nPages = 64
	dirty := pageRange(nPages)
	w, _, sink, _ := newTestWindow(t, nPages, dirty, nil)

	var eg errgroup.Group
	eg.Go(func() error { return w.Sweep(t.Context()) })
	for range 8 {
		eg.Go(func() error {
			for i := range nPages {
				if _, err := w.EnsureCopied(t.Context(), uint32(i)); err != nil {
					return err
				}
			}

			return nil
		})
	}
	require.NoError(t, eg.Wait())
	require.NoError(t, w.Wait(t.Context()))

	for i := range nPages {
		assert.Equal(t, 1, sink.writeCount(int64(i)*testCoWPageSize), "page %d written exactly once", i)
	}
}

func TestCoWWindowCancelDegradesToNoop(t *testing.T) {
	t.Parallel()

	dirty := pageRange(16)
	w, _, sink, _ := newTestWindow(t, 16, dirty, nil)

	w.Cancel(errors.New("checkpoint aborted"))

	handled, err := w.EnsureCopied(t.Context(), 3)
	require.NoError(t, err, "canceled window must be a no-op, not an error")
	assert.False(t, handled, "canceled window owns no pages")
	assert.Equal(t, 0, sink.writeCount(3*testCoWPageSize), "no copy after cancel")

	require.ErrorIs(t, w.Wait(t.Context()), ErrCoWWindowCanceled)

	require.ErrorIs(t, w.Sweep(t.Context()), ErrCoWWindowCanceled, "sweep on a canceled window resolves to the cancel error")
}

func TestCoWWindowSinkErrorCancelsWindow(t *testing.T) {
	t.Parallel()

	dirty := pageRange(8)
	w, _, sink, _ := newTestWindow(t, 8, dirty, nil)
	sink.failAt = 4 * testCoWPageSize

	err := w.Sweep(t.Context())
	require.ErrorIs(t, err, ErrCoWWindowCanceled)
	require.ErrorIs(t, w.Wait(t.Context()), ErrCoWWindowCanceled)

	// The guest-facing path must stay unblocked after the failure.
	_, err = w.EnsureCopied(t.Context(), 7)
	require.NoError(t, err)
}

// TestCoWWindowTrackerRebaseline verifies the tracker interplay: captured
// pages demote Dirty→Clean, and a write racing the sweep (MarkWritten after
// EnsureCopied, per the resolve ordering) ends Dirty for the next interval.
func TestCoWWindowTrackerRebaseline(t *testing.T) {
	t.Parallel()

	const nPages = 32
	tracker := block.NewTracker()
	tracker.SetRange(0, nPages, block.Dirty)

	dirty := pageRange(nPages)
	w, _, sink, original := newTestWindow(t, nPages, dirty, tracker)

	// Simulate the resolve on page 5: pre-image copy, then the promotion.
	handled, err := w.EnsureCopied(t.Context(), 5)
	require.NoError(t, err)
	require.True(t, handled, "page 5 belongs to the window")
	tracker.MarkWritten(5)

	require.NoError(t, w.Sweep(t.Context()))
	require.NoError(t, w.Wait(t.Context()))

	for i := range uint32(nPages) {
		want := block.Clean
		if i == 5 {
			want = block.Dirty
		}
		assert.Equal(t, want, tracker.Get(i), "page %d state after sweep", i)
	}
	assert.Equal(t, original, sink.data, "rebaseline never changes what is exported")
}

func TestCoWWindowEmptySetResolvesImmediately(t *testing.T) {
	t.Parallel()

	w, _, sink, _ := newTestWindow(t, 4, nil, nil)
	require.NoError(t, w.Wait(t.Context()))
	require.NoError(t, w.Sweep(t.Context()))
	assert.Empty(t, sink.writes, "nothing to capture, nothing written")
}

// TestCoWWindowOverlaps covers the REMOVE tripwire predicate: ANY window
// page in range trips it, captured or not — the punch precedes the serve
// loop's check, so a racing copy may hold post-punch zeros and captured
// state must not wave a REMOVE through. Only a canceled window goes quiet.
func TestCoWWindowOverlaps(t *testing.T) {
	t.Parallel()

	dirty := []uint32{2, 5, 9}
	w, _, sink, _ := newTestWindow(t, 16, dirty, nil)

	assert.False(t, w.Overlaps(0, 2), "below the set")
	assert.False(t, w.Overlaps(3, 5), "gap between set pages")
	assert.True(t, w.Overlaps(0, 16), "window pages in range")
	assert.True(t, w.Overlaps(5, 6), "exact window page")
	assert.False(t, w.Overlaps(10, 16), "above the set")

	// Capturing a page must NOT quiet it: whether its copy beat the punch is
	// unknowable, so the tripwire stays fail-closed.
	handled, err := w.EnsureCopied(t.Context(), 5)
	require.NoError(t, err)
	require.True(t, handled)
	assert.True(t, w.Overlaps(5, 6), "captured page still trips")

	require.NoError(t, w.Sweep(t.Context()))
	assert.True(t, w.Overlaps(0, 16), "fully captured window still trips")
	assert.Len(t, sink.writes, len(dirty), "every window page captured once")

	// A canceled window owns nothing: the tripwire must not fire (the export
	// already failed; REMOVEs are then business as usual).
	w2, _, sink2, _ := newTestWindow(t, 16, dirty, nil)
	w2.Cancel(errors.New("boom"))
	assert.False(t, w2.Overlaps(0, 16))
	assert.Empty(t, sink2.writes, "canceled window captured nothing")
}

// TestCoWWindowManyRounds shuffles claim contention across many small rounds
// to shake out ordering bugs under the race detector.
func TestCoWWindowManyRounds(t *testing.T) {
	t.Parallel()

	for round := range 20 {
		nPages := 16 + round
		dirty := pageRange(nPages)
		w, src, sink, original := newTestWindow(t, nPages, dirty, nil)

		var eg errgroup.Group
		eg.Go(func() error { return w.Sweep(t.Context()) })
		for i := range nPages {
			eg.Go(func() error {
				idx := uint32(i)
				if _, err := w.EnsureCopied(t.Context(), idx); err != nil {
					return fmt.Errorf("page %d: %w", idx, err)
				}
				src[int64(idx)*testCoWPageSize] = 0xAA

				return nil
			})
		}
		require.NoError(t, eg.Wait())
		require.NoError(t, w.Wait(t.Context()))
		require.Equal(t, original, sink.data, "round %d pre-image", round)
	}
}

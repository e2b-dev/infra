//go:build linux

package userfaultfd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// ErrCoWWindowCanceled resolves the window's Wait when the export was
// canceled before every page was captured.
var ErrCoWWindowCanceled = errors.New("memory CoW window canceled")

// CoWWindow captures the pause-time content of a fixed page set from a
// running VM: every page in the set is write-protect-armed while the VM is
// still paused, and after resume each page reaches the sink exactly once —
// either via the background sweep or via the WP-fault path copying the
// pre-image BEFORE the blocked writer is unprotected and woken. The window is
// deliberately ignorant of userfaultfd and of where pages go: src/dst are
// plain io interfaces, so future consumers (fork sources, migration streams)
// only swap the sink.
//
// Concurrency: the copied bitmap gives the fault path a lock-free fast path
// for already-captured pages (the common case as the sweep progresses); only
// contended claims (fault vs sweep on the same page, sub-millisecond copy)
// touch the mutex. A claim loser waits for the winner's copy to complete
// before returning, so an unprotect never races a half-read pre-image.
//
// Tracker interplay (tracker-based dirty source): a captured page is
// rebaselined Dirty→Clean (MarkExported) BEFORE claim waiters are released,
// so a writer's MarkWritten — which always runs after EnsureCopied returns —
// lands after the rebaseline and the page is correctly Dirty for the next
// interval. Pages the guest never rewrites stay Clean; the sweep leaves them
// armed. NOTE: exact minimal diffs across REPEATED in-place checkpoints
// additionally require the diff chain to parent on the previous checkpoint's
// header — today in-place diffs parent on the original template, so the
// caller must export cumulatively (see Sandbox.inPlaceExportedDirty).
//
// REMOVE (madvise) is NOT handled: a REMOVE zapping an uncopied page would
// export zeros where pause-time content is owed. The caller must guarantee
// mutual exclusion with continuous REMOVE sources (the checkpoint pauses
// balloon free-page reporting for the window's lifetime; the synchronous
// pre-pause hinting drain settles before the window arms and needs no
// pause). The serve loop's REMOVE batch enforces this as a tripwire: a
// REMOVE overlapping an uncaptured window page cancels the window (see
// Overlaps), failing the export cleanly instead of corrupting it.
type CoWWindow struct {
	// pages is the captured set (memfile page indices). Immutable.
	pages    *roaring.Bitmap
	pageSize int64

	src io.ReaderAt
	dst io.WriterAt

	// tracker, when non-nil, receives the Dirty→Clean rebaseline per
	// captured page.
	tracker *block.Tracker

	// copied marks pages whose pre-image reached the sink; lock-free reads.
	copied atomicBitmap

	// claims holds an in-flight copy per page; losers wait on the channel.
	mu     sync.Mutex
	claims map[uint32]chan struct{}

	remaining atomic.Int64
	canceled  atomic.Bool

	// cancelCause holds the FIRST cancel's error independently of done:
	// done is first-setter-wins, so a final-page completion can beat a
	// concurrent Cancel's SetError — but a cancel decision (e.g. the REMOVE
	// tripwire, whose racing copy may have read post-punch zeros) must stay
	// authoritative over a raced success. Sweep/Wait re-check it after done
	// resolves.
	cancelCause atomic.Pointer[error]

	// done resolves when every page is captured (value) or the window is
	// canceled (error).
	done *utils.SetOnce[struct{}]
}

// NewCoWWindow builds a window over the given page set. src offsets and dst
// offsets are both memfile byte offsets (identity mapping, like the memfd).
func NewCoWWindow(pages *roaring.Bitmap, pageSize int64, src io.ReaderAt, dst io.WriterAt, tracker *block.Tracker) *CoWWindow {
	maxIdx := uint32(0)
	if !pages.IsEmpty() {
		maxIdx = pages.Maximum()
	}

	w := &CoWWindow{
		pages:    pages,
		pageSize: pageSize,
		src:      src,
		dst:      dst,
		tracker:  tracker,
		copied:   newAtomicBitmap(maxIdx + 1),
		claims:   make(map[uint32]chan struct{}),
		done:     utils.NewSetOnce[struct{}](),
	}
	w.remaining.Store(int64(pages.GetCardinality()))
	if pages.IsEmpty() {
		_ = w.done.SetValue(struct{}{})
	}

	return w
}

// EnsureCopied guarantees the pre-image of the page at idx is in the sink
// before returning, when the page belongs to the window. The WP-fault path
// MUST call this before unprotecting the page: the faulting writer is still
// blocked, so the bytes read here are the pause-time content by construction.
//
// handled reports whether the window owned the page (in the set and not
// canceled) — callers use it to classify the resolve. When it is false the
// caller proceeds with the plain tracking-only resolve (a canceled window
// degrades to exactly today's behavior; armed but unowned pages are
// harmless). The only error is a dead ctx while waiting on a claim winner.
func (w *CoWWindow) EnsureCopied(ctx context.Context, idx uint32) (handled bool, err error) {
	return w.ensureCopied(ctx, idx, false)
}

// ensureCopied is EnsureCopied with the capture-path attribution: viaSweep
// marks copies performed by the background sweep; everything else is the
// fault path. Only the claim WINNER records the capture metric — losers wait
// on an existing copy.
func (w *CoWWindow) ensureCopied(ctx context.Context, idx uint32, viaSweep bool) (handled bool, err error) {
	if !w.pages.Contains(idx) || w.canceled.Load() {
		return false, nil
	}
	if w.copied.get(idx) {
		return true, nil
	}

	w.mu.Lock()
	if w.copied.get(idx) || w.canceled.Load() {
		canceled := w.canceled.Load()
		w.mu.Unlock()

		return !canceled, nil
	}
	ch, inflight := w.claims[idx]
	if inflight {
		w.mu.Unlock()
		// A claim loser waits for the winner (fault or sweep) to finish the
		// copy; returning earlier would let the caller unprotect and wake the
		// writer while the winner is still reading the pre-image. The winner
		// always closes the channel, even when its copy fails and cancels
		// the window.
		select {
		case <-ch:
			return true, nil
		case <-ctx.Done():
			return true, ctx.Err()
		}
	}
	ch = make(chan struct{})
	w.claims[idx] = ch
	w.mu.Unlock()

	w.copyPage(ctx, idx)
	// Claim winner: attribute the capture to the path that performed it. A
	// failed copy cancels the window, so the count stays honest either way —
	// canceled windows never resolve success.
	if w.copied.get(idx) {
		if viaSweep {
			cowCaptureCount.Add(ctx, 1, cowCaptureSweepAttrs)
		} else {
			cowCaptureCount.Add(ctx, 1, cowCaptureFaultAttrs)
		}
	}

	w.mu.Lock()
	delete(w.claims, idx)
	w.mu.Unlock()
	close(ch)

	return true, nil
}

// copyPage moves one page's pre-image to the sink and does the bookkeeping.
// A copy failure cancels the whole window (the export is no longer complete)
// but never blocks the caller: the guest-facing resolve continues regardless,
// only the checkpoint fails.
func (w *CoWWindow) copyPage(ctx context.Context, idx uint32) {
	off := int64(idx) * w.pageSize

	// Fast path: a source that exposes its backing bytes directly (the memfd
	// view does) skips the intermediate buffer, halving memory traffic per
	// captured page — this runs alongside live guests and on the fault path,
	// where the extra copy doubles the added stall. The io interfaces stay
	// as the fallback for future sources.
	if sl, ok := w.src.(interface {
		Slice(off, length int64) ([]byte, error)
	}); ok {
		src, err := sl.Slice(off, w.pageSize)
		if err != nil {
			recordCoWCancel(ctx, "source_read_error")
			w.Cancel(fmt.Errorf("CoW window: slice page %d: %w", idx, err))

			return
		}
		// src is LIVE guest memory here, and the sink may read it more than
		// once (the packed cache sink runs IsZero and then copy). Sound only
		// because window pages are WP-armed under sync-WP: a guest write
		// faults and blocks until this page's EnsureCopied returns, so the
		// bytes cannot change while the sink reads them. A source whose
		// content can mutate mid-capture must NOT expose Slice.
		if _, err := w.dst.WriteAt(src, off); err != nil {
			recordCoWCancel(ctx, "sink_write_error")
			w.Cancel(fmt.Errorf("CoW window: write page %d: %w", idx, err))

			return
		}
		w.finishCopy(idx)

		return
	}

	var buf []byte
	if w.pageSize == int64(header.HugepageSize) {
		bufPtr := pagePool.Get().(*[]byte)
		defer pagePool.Put(bufPtr)
		buf = (*bufPtr)[:w.pageSize]
	} else {
		buf = make([]byte, w.pageSize)
	}

	if _, err := io.ReadFull(io.NewSectionReader(w.src, off, w.pageSize), buf); err != nil {
		recordCoWCancel(ctx, "source_read_error")
		w.Cancel(fmt.Errorf("CoW window: read page %d: %w", idx, err))

		return
	}
	if _, err := w.dst.WriteAt(buf, off); err != nil {
		recordCoWCancel(ctx, "sink_write_error")
		w.Cancel(fmt.Errorf("CoW window: write page %d: %w", idx, err))

		return
	}

	w.finishCopy(idx)
}

// finishCopy does the post-copy bookkeeping shared by both copyPage paths.
func (w *CoWWindow) finishCopy(idx uint32) {
	// Rebaseline BEFORE releasing claim waiters (and before this fault-path
	// caller returns into its MarkWritten): the captured content is the layer
	// just exported, so the page leaves the next interval's dirty set unless
	// a later write promotes it back.
	if w.tracker != nil {
		w.tracker.MarkExported(idx)
	}

	// Resolve done BEFORE publishing the copied bit: a fast-path reader that
	// observes copied for the final page must also observe the window as
	// complete. The claim entry is still installed, so no concurrent copy
	// can slip in between the two.
	if w.remaining.Add(-1) == 0 {
		_ = w.done.SetValue(struct{}{})
	}
	w.copied.set(idx)
}

// Sweep drains the window in the background: every not-yet-captured page is
// copied to the sink. Pages stay ARMED after their copy — the next guest
// write takes a tracking-only WP fault and re-dirties the page for the next
// interval. Returns when the window is fully captured, canceled, or ctx ends.
func (w *CoWWindow) Sweep(ctx context.Context) error {
	it := w.pages.Iterator()
	for it.HasNext() {
		if w.canceled.Load() {
			break
		}
		if err := ctx.Err(); err != nil {
			recordCoWCancel(ctx, "sweep_interrupted")
			w.Cancel(fmt.Errorf("CoW window: sweep interrupted: %w", err))

			break
		}
		if _, err := w.ensureCopied(ctx, it.Next(), true); err != nil {
			w.Cancel(fmt.Errorf("CoW window: sweep copy: %w", err))

			break
		}
	}

	// Every page is captured, in flight on a fault-path worker, or the
	// window is canceled — all of which resolve done. Block (with ctx as a
	// backstop) rather than peeking: a fault-path winner may still be a few
	// instructions from resolving.
	_, err := w.done.WaitWithContext(ctx)
	// A cancel that lost the done race to the final copy's completion is
	// still authoritative — the copy it raced may have captured post-punch
	// zeros as pause-time content.
	if cause := w.CancelCause(); cause != nil {
		return cause
	}

	return err
}

// Cancel aborts the export: the first error wins and resolves Wait; the
// fault path degrades to tracking-only resolves from here on. In-flight
// copies finish on their own and still release their waiters. The cause is
// recorded independently of done, so a cancel remains authoritative even
// when the final copy's completion wins the done race (see cancelCause).
func (w *CoWWindow) Cancel(err error) {
	wrapped := fmt.Errorf("%w: %w", ErrCoWWindowCanceled, err)
	w.cancelCause.CompareAndSwap(nil, &wrapped)
	w.canceled.Store(true)
	_ = w.done.SetError(wrapped)
}

// CancelCause returns the first cancel's error, or nil if the window was
// never canceled.
func (w *CoWWindow) CancelCause() error {
	if p := w.cancelCause.Load(); p != nil {
		return *p
	}

	return nil
}

// RecordCancelReason records a window-cancel reason for callers outside this
// package that drive Cancel/CancelAndDrain themselves (e.g. the pause-abort
// cleanup). In-package cancel sites record their reason directly.
func (w *CoWWindow) RecordCancelReason(ctx context.Context, reason string) {
	recordCoWCancel(ctx, reason)
}

// Wait blocks until every page is captured or the window is canceled.
func (w *CoWWindow) Wait(ctx context.Context) error {
	_, err := w.done.WaitWithContext(ctx)
	if cause := w.CancelCause(); cause != nil {
		return cause
	}

	return err
}

// CancelAndDrain cancels the window and blocks until no copy is in flight,
// so the caller can safely invalidate src afterwards (e.g. unmap the memfd
// the window reads pre-images from). New EnsureCopied/Sweep calls become
// no-ops the moment the cancel lands; this only waits out claims that were
// already copying.
func (w *CoWWindow) CancelAndDrain(err error) {
	w.Cancel(err)
	for {
		var ch chan struct{}
		w.mu.Lock()
		for _, c := range w.claims {
			ch = c

			break
		}
		w.mu.Unlock()
		if ch == nil {
			return
		}
		<-ch
	}
}

// Copied reports how many pages have reached the sink so far.
func (w *CoWWindow) Copied() int64 {
	return int64(w.pages.GetCardinality()) - w.remaining.Load()
}

// Overlaps reports whether [start, end) intersects the window's page set —
// captured or not. The REMOVE batch uses it as the corruption tripwire, and
// it deliberately does NOT consult the copied bits: whichever side of the
// event read the kernel's punch lands on, it is not ordered against the
// window's copies, so a racing sweep or fault-path copy can read post-punch
// zeros, write them to the sink and mark the page captured — making any
// captured-state check wave the REMOVE through over a corrupt capture.
// Whether an overlapping page's copy beat the punch is unknowable from user
// space, so the tripwire fails CLOSED on any overlap: the window is canceled,
// the checkpoint fails cleanly, and a retry snapshots a newer consistent
// state. REMOVEs should never hit a live window — free-page reporting is
// paused for its lifetime — so this guards against gaps in that pause, not a
// normal path; the cost of a spurious cancel on a safely-captured page is one
// failed checkpoint, against silent corruption the other way.
func (w *CoWWindow) Overlaps(start, end uint32) bool {
	if w.canceled.Load() {
		return false
	}
	it := w.pages.Iterator()
	it.AdvanceIfNeeded(start)

	return it.HasNext() && it.PeekNext() < end
}

// atomicBitmap is a fixed-size lock-free bitmap (one bit per page index).
type atomicBitmap []atomic.Uint64

func newAtomicBitmap(bits uint32) atomicBitmap {
	return make(atomicBitmap, (uint64(bits)+63)/64)
}

func (b atomicBitmap) get(i uint32) bool {
	return b[i/64].Load()&(1<<(i%64)) != 0
}

func (b atomicBitmap) set(i uint32) {
	b[i/64].Or(1 << (i % 64))
}

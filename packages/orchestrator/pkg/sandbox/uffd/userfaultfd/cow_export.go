//go:build linux

package userfaultfd

import (
	"context"
	"fmt"
	"io"

	"github.com/RoaringBitmap/roaring/v2"
)

// PackedPageSink adapts a packed-layout diff artifact to the CoWWindow's
// identity-addressed writes. The window writes each page at its memfile
// offset; diff artifacts store the dirty pages CONCATENATED in ascending
// page order (see header.CreateMapping / copyFromMemfd), so the sink
// translates identity offset → rank(page) * pageSize. The page set must be
// the window's set (immutable), or the ranks would not match the header.
type PackedPageSink struct {
	pages    *roaring.Bitmap
	pageSize int64
	dst      io.WriterAt
}

func NewPackedPageSink(pages *roaring.Bitmap, pageSize int64, dst io.WriterAt) *PackedPageSink {
	return &PackedPageSink{pages: pages, pageSize: pageSize, dst: dst}
}

func (s *PackedPageSink) WriteAt(p []byte, off int64) (int, error) {
	if off%s.pageSize != 0 || int64(len(p)) != s.pageSize {
		return 0, fmt.Errorf("packed sink: write [%d,%d) is not one aligned page", off, off+int64(len(p)))
	}
	idx := uint32(off / s.pageSize)
	if !s.pages.Contains(idx) {
		return 0, fmt.Errorf("packed sink: page %d is not in the captured set", idx)
	}
	// Rank is 1-based; the page's slot in the packed artifact is Rank-1.
	packedOff := int64(s.pages.Rank(idx)-1) * s.pageSize

	return s.dst.WriteAt(p, packedOff)
}

// wpRun is a contiguous host-virtual range to arm in one ioctl.
type wpRun struct {
	start  uintptr
	length uintptr
}

// hostVirtTranslator maps a memfile byte offset to its host virtual address.
// Satisfied by *memory.Mapping; narrowed to an interface so the run
// coalescing is unit-testable without a live mapping.
type hostVirtTranslator interface {
	GetHostVirtAddr(off int64) (uintptr, error)
}

// coalesceWPRuns translates every page in the set to its host address and
// merges pages that are contiguous in HOST space into single runs. Guest
// pages contiguous in memfile space may live in different mapped regions, so
// contiguity is decided on the translated addresses, not the indices.
func coalesceWPRuns(pages *roaring.Bitmap, ma hostVirtTranslator, pageSize uintptr) ([]wpRun, error) {
	runs := make([]wpRun, 0, 8)
	var run wpRun

	it := pages.Iterator()
	for it.HasNext() {
		idx := it.Next()
		off := int64(idx) * int64(pageSize)
		addr, err := ma.GetHostVirtAddr(off)
		if err != nil {
			return nil, fmt.Errorf("translate page %d (offset %#x): %w", idx, off, err)
		}

		if run.length > 0 && run.start+run.length == addr {
			run.length += pageSize

			continue
		}
		if run.length > 0 {
			runs = append(runs, run)
		}
		run = wpRun{start: addr, length: pageSize}
	}
	if run.length > 0 {
		runs = append(runs, run)
	}

	return runs, nil
}

// ArmWriteProtect write-protects every page in the set. MUST be called while
// the guest is paused: with no vCPU running the arm cannot race a guest
// write, so no readout↔arm window exists. No wake flag is involved — there
// are no waiters to wake.
func (u *Userfaultfd) ArmWriteProtect(pages *roaring.Bitmap) error {
	runs, err := coalesceWPRuns(pages, u.ma, u.pageSize)
	if err != nil {
		return fmt.Errorf("arm write-protect: %w", err)
	}
	for _, r := range runs {
		if err := u.fd.writeProtectRange(r.start, r.length, u.pageSize, UFFDIO_WRITEPROTECT_MODE_WP); err != nil {
			return fmt.Errorf("arm write-protect [%#x, %#x): %w", r.start, r.start+r.length, err)
		}
	}

	return nil
}

// BeginCoWExport arms the page set for write-protection and installs a CoW
// window over it: from here on, guest writes to the set are pre-image-copied
// into sink before they proceed (see resolveWriteProtect), and the caller
// drives the background Sweep after the guest resumes.
//
// MUST be called while the guest is paused. The window reads pre-images from
// src (the borrowed memfd view) — the caller guarantees src outlives the
// window (cancel or wait it out before teardown closes the memfd).
func (u *Userfaultfd) BeginCoWExport(pages *roaring.Bitmap, src io.ReaderAt, sink io.WriterAt) (*CoWWindow, error) {
	if err := u.ArmWriteProtect(pages); err != nil {
		return nil, err
	}

	// Clone: the caller's bitmap is also the header's dirty set; the window
	// must keep an immutable view.
	w := NewCoWWindow(pages.Clone(), int64(u.pageSize), src, sink, u.pageTracker)
	u.cowWindow.Store(w)

	return w, nil
}

// EndCoWExport uninstalls the window if it is still the active one, so a
// finished (or canceled) export stops pinning its sink and memfd view. Armed
// pages stay armed — their next write is a plain tracking-only resolve.
//
// Taken under readSerial — the lock the serve loop holds across its ENTIRE
// read → parse → tripwire-cancel section — not merely settleRequests (which
// the serve loop only takes for the parse-and-cancel half). The distinction
// is the whole safety argument: the madvising thread is released by the
// event READ, so a zap (and therefore a corrupt post-punch copy) can only
// happen at or after a read that is inside a readSerial critical section.
// Whichever side of the kernel's punch/notify ordering applies, by the time
// EndCoWExport acquires readSerial every read that could have released a zap
// has also completed its tripwire pass, so any cancel of this window has
// fully landed (visible to CancelCause) and no later tripwire can target it.
// Under settleRequests alone there was a hole: a REMOVE read but not yet
// parsed let a sweep complete, the runner uninstall the window, and the
// cancel fire into nil — accepting a corrupt artifact.
func (u *Userfaultfd) EndCoWExport(w *CoWWindow) {
	u.readSerial.Lock()
	defer u.readSerial.Unlock()
	u.cowWindow.CompareAndSwap(w, nil)
}

// CancelActiveCoWWindow cancels and drains the installed window, if any.
// The uffd teardown calls this BEFORE unmapping the memfd the window reads
// pre-images from — an in-flight copy reading an unmapped view would fault
// the orchestrator itself.
func (u *Userfaultfd) CancelActiveCoWWindow(ctx context.Context, err error) {
	if w := u.cowWindow.Swap(nil); w != nil {
		recordCoWCancel(ctx, "teardown")
		w.CancelAndDrain(err)
	}
}

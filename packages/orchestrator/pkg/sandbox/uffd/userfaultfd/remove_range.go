//go:build linux

package userfaultfd

import (
	"github.com/RoaringBitmap/roaring/v2"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

// applyRemoveRange folds one UFFD_EVENT_REMOVE byte range into the page
// tracker. Only blocks fully covered by [startOff, startOff+length) are
// marked Zero: the kernel does not guarantee the event range is aligned to
// the tracker page size (madvise ranges are 4 KiB-granular, and on hugetlbfs
// the hole punch discards only whole hugepages while zeroing the partial
// edges in place — yet the event reports the full requested range).
//
// A partially covered block in Dirty state stays Dirty (its page is still
// mapped) and is recorded in tainted: its content is now a mix of live data
// and kernel-zeroed bytes, so the next pause must re-read it from the memfd
// instead of trusting tracker state — marking it Zero would zero live data
// on resume, and leaving it untainted would resume stale pre-discard bytes.
// Partial blocks in Zero or NotPresent state are left untouched: Zero
// already reads as zeros, and a NotPresent block keeps resolving to the
// parent, which is acceptable for pages the guest declared free.
//
// The caller must hold settleRequests.Lock (same as the Zero SetRange in
// the serve loop's REMOVE batch).
func applyRemoveRange(tracker *block.Tracker, tainted *roaring.Bitmap, startOff, length, pageSize int64) (zeroedBlocks, taintedBlocks int) {
	end := startOff + length
	zeroStart := (startOff + pageSize - 1) / pageSize
	zeroEnd := end / pageSize
	if zeroEnd > zeroStart {
		tracker.SetRange(uint32(zeroStart), uint32(zeroEnd), block.Zero)
		zeroedBlocks = int(zeroEnd - zeroStart)
	}

	taint := func(idx int64) {
		if tracker.Get(uint32(idx)) == block.Dirty {
			tainted.Add(uint32(idx))
			taintedBlocks++
		}
	}
	headPartial := startOff%pageSize != 0
	tailPartial := end%pageSize != 0
	if headPartial {
		taint(startOff / pageSize)
	}
	if tailPartial && (!headPartial || end/pageSize != startOff/pageSize) {
		taint(end / pageSize)
	}

	return zeroedBlocks, taintedBlocks
}

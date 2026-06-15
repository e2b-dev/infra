//go:build linux

package userfaultfd

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestApplyRemoveRange(t *testing.T) {
	t.Parallel()

	const page = int64(header.HugepageSize)

	run := func(name string, dirty []uint32, startOff, length int64, wantZero, wantTaint []uint32) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tracker := block.NewTracker()
			for _, b := range dirty {
				tracker.SetRange(b, b+1, block.Dirty)
			}
			tainted := roaring.New()

			applyRemoveRange(tracker, tainted, startOff, length, page)

			for _, b := range wantZero {
				assert.Equal(t, block.Zero, tracker.Get(b), "block %d should be Zero", b)
			}
			assert.ElementsMatch(t, wantTaint, tainted.ToArray(), "tainted blocks")
			// Tainted blocks must keep their Dirty state (still present).
			for _, b := range wantTaint {
				assert.Equal(t, block.Dirty, tracker.Get(b), "tainted block %d must stay Dirty", b)
			}
		})
	}

	// Aligned range: all covered blocks Zero, nothing tainted (today's FC).
	run("aligned", []uint32{0, 1, 2}, 0, 2*page, []uint32{0, 1}, nil)

	// Misaligned head into a Dirty block: block 0 holds live data outside the
	// range — it must NOT be zeroed, and must be tainted for the pause scan.
	run("misaligned head over dirty", []uint32{0, 1, 2}, page/2, page+page/2, []uint32{1}, []uint32{0})

	// Misaligned tail into a Dirty block.
	run("misaligned tail over dirty", []uint32{0, 1, 2}, 0, 2*page+page/2, []uint32{0, 1}, []uint32{2})

	// Both edges misaligned.
	run("both edges misaligned", []uint32{0, 1, 2, 3}, page/2, 3*page, []uint32{1, 2}, []uint32{0, 3})

	// Sub-block range fully inside one Dirty block: nothing zeroed, tainted.
	run("sub-block inside dirty", []uint32{0}, page/4, page/2, nil, []uint32{0})

	// Aligned start, short misaligned end within the same block.
	run("aligned start short end", []uint32{0}, 0, page/2, nil, []uint32{0})

	// Partial edges over NotPresent blocks: left untouched, not tainted
	// (a NotPresent block keeps resolving to the parent).
	run("misaligned over not-present", nil, page/2, 2*page, []uint32{1}, nil)
}

//go:build linux

package block

import (
	"bytes"
	"context"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestDedup_DivergentParentCacheIsBakedIntoSnapshot demonstrates a dedup-only
// corruption amplification mechanism: dedupCompare drops a page when it equals
// the *pause node's local view* of the parent (base.Slice → chunker cache). If
// that local view diverges from the authoritative parent object — a partial
// fetch marked cached, a frame written at the wrong offset, disk/mmap
// corruption — the guest itself runs consistently (UFFD served it the same
// local bytes), but the deduped snapshot keeps the parent mapping, so any
// resume reading the authoritative parent serves different bytes than the
// guest had at pause. The non-dedup path stores the guest's actual bytes and
// is immune.
//
// This is a mechanism demonstration, not a regression test of a specific bug:
// it documents that dedup converts transient node-local parent-read
// divergence into permanent snapshot corruption, which matches the
// "resume has wrong memory, all reads local, failure follows the snapshot"
// production signature.
func TestDedup_DivergentParentCacheIsBakedIntoSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2

	// Authoritative parent content (what remote storage / other nodes hold).
	authoritative := make([]byte, size)
	for i := range authoritative {
		authoritative[i] = byte(i % 251)
	}

	// The pause node's local chunker view diverges on page 1: one flipped run.
	localView := append([]byte(nil), authoritative...)
	copy(localView[pageSize:pageSize+64], bytes.Repeat([]byte{0xC3}, 64))

	// Guest memory: served from the local view during the session (UFFD reads
	// through the same chunker), then the guest writes page 2, dirtying block 0.
	guest := append([]byte(nil), localView...)
	for i := 2 * pageSize; i < 3*pageSize; i++ {
		guest[i] = 0xAB
	}

	dirty := roaring.New()
	dirty.Add(0) // block 0 contains pages 0..3

	src := func(absOff int64) ([]byte, error) { return guest[absOff : absOff+blockSize], nil }
	base := &fakeOriginalDevice{data: localView}

	plan, err := dedupCompare(ctx, src, base, dirty, blockSize, false, DedupBudget{})
	require.NoError(t, err)

	// Page 1 equals the local view → dedup drops it to the parent mapping,
	// even though the local view disagrees with the authoritative parent.
	require.False(t, plan.pageDirty.Contains(1),
		"page 1 matched the local parent view and was deduped away")
	require.True(t, plan.pageDirty.Contains(2), "the written page is stored")

	// Resume on any node with the authoritative parent: page 1 reads
	// authoritative bytes, which differ from what the guest actually had.
	restoredPage1 := authoritative[pageSize : 2*pageSize]
	guestPage1 := guest[pageSize : 2*pageSize]
	require.False(t, bytes.Equal(restoredPage1, guestPage1),
		"deduped snapshot restores different bytes than the guest had at pause")

	// The non-dedup path (store every non-zero page of the dirty block) would
	// have captured the guest's actual bytes and restored them faithfully.
	nonDedupStored := roaring.New()
	for r := range BitsetRanges(dirty, blockSize) {
		for p := r.Start / pageSize; p < r.End()/pageSize; p++ {
			if !header.IsZero(guest[p*pageSize : (p+1)*pageSize]) {
				nonDedupStored.Add(uint32(p))
			}
		}
	}
	require.True(t, nonDedupStored.Contains(1),
		"non-dedup stores the guest's page 1 verbatim — immune to parent divergence")
}

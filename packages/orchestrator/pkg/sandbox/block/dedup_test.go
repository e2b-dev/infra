//go:build linux

package block

import (
	"crypto/rand"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestCacheDedup_FetchRunBudgetPromotesSmallParentRun(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize

	baseData := make([]byte, size)
	_, err := rand.Read(baseData)
	require.NoError(t, err)
	srcData := make([]byte, size)
	copy(srcData, baseData)
	srcData[0] ^= 0xFF
	srcData[2*pageSize] ^= 0xFF
	clear(srcData[3*pageSize : 4*pageSize])

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(t.Context(), &fakeOriginalDevice{data: baseData}, dirty, blockSize, t.TempDir()+"/dedup", false, false, DedupBudget{MaxFetchWindowsPerBlock: 1, MaxPromotedParentPagesPerBlock: 1, FetchRunWindowPages: 4})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 3, meta.Dirty.GetCardinality())
	require.EqualValues(t, 1, meta.Empty.GetCardinality())

	for _, i := range []int64{0, 1, 2} {
		got := make([]byte, pageSize)
		_, err := cache.ReadAt(got, i*pageSize)
		require.NoError(t, err)
		require.Equal(t, srcData[i*pageSize:(i+1)*pageSize], got, "promoted page %d", i)
	}
}

func TestDedupPlanPromoteCheapFrames(t *testing.T) {
	t.Parallel()

	buildA, buildB := uuid.New(), uuid.New()
	newPlan := func() *dedupPlan { return &dedupPlan{pageDirty: roaring.New(), pageEmpty: roaring.New()} }

	t.Run("frames at or under the price threshold", func(t *testing.T) {
		t.Parallel()

		p := newPlan()
		p.promoteCheapFrames(map[dedupFetchKey]*roaring.Bitmap{
			{buildID: buildA, window: 0}: roaring.BitmapOf(10, 11, 12),
			{buildID: buildA, window: 1}: roaring.BitmapOf(20),
			{buildID: buildB, window: 0}: roaring.BitmapOf(30, 31),
		}, nil, 512, DedupBudget{MaxPagesPerPromotedFrame: 2, FetchRunWindowPages: 512})

		// The 1-page and 2-page keys are at or under the threshold; the
		// 3-page key costs too much.
		require.EqualValues(t, 2, p.promotedFrames)
		require.EqualValues(t, 3, p.promotedFramePages)
		require.True(t, p.pageDirty.Contains(20))
		require.True(t, p.pageDirty.Contains(30))
		require.True(t, p.pageDirty.Contains(31))
		require.False(t, p.pageDirty.Contains(10))
	})

	t.Run("external references discount the expected value", func(t *testing.T) {
		t.Parallel()

		key := dedupFetchKey{buildID: buildA, window: 0}
		budget := DedupBudget{MaxPagesPerPromotedFrame: 4, BlockFaultPct: 50, FetchRunWindowPages: 512}

		// 1 internal + 1 external block: value 0.5*0.5, price 4*0.25 = 1 page.
		p := newPlan()
		p.promoteCheapFrames(map[dedupFetchKey]*roaring.Bitmap{key: roaring.BitmapOf(0)}, map[dedupFetchKey]int{key: 1}, 4, budget)
		require.EqualValues(t, 1, p.promotedFrames)

		// A second external block halves the value below the 1-page cost.
		p = newPlan()
		p.promoteCheapFrames(map[dedupFetchKey]*roaring.Bitmap{key: roaring.BitmapOf(0)}, map[dedupFetchKey]int{key: 2}, 4, budget)
		require.EqualValues(t, 0, p.promotedFrames)
	})

	t.Run("full-window keys are never promoted", func(t *testing.T) {
		t.Parallel()

		p := newPlan()
		p.promoteCheapFrames(map[dedupFetchKey]*roaring.Bitmap{
			{buildID: buildA, window: 0}: roaring.BitmapOf(0, 1, 2, 3),
		}, nil, 512, DedupBudget{MaxPagesPerPromotedFrame: 100, FetchRunWindowPages: 4})

		require.EqualValues(t, 0, p.promotedFrames)
		require.True(t, p.pageDirty.IsEmpty())
	})
}

// Exercises both budget passes together. The global threshold (1 page) runs
// first and promotes block 0's cheap frame (page 1) but not block 1's 2-page
// frame; the per-block cap then sees the final layout and compacts block 1 by
// promoting pages 6,7 into the current window.
func TestCacheDedup_PerBlockAndGlobalBudgetsCompose(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := 2 * blockSize

	baseData := make([]byte, size)
	_, err := rand.Read(baseData)
	require.NoError(t, err)
	srcData := make([]byte, size)
	copy(srcData, baseData)
	// Block 0: page 1 matches base (1-page frame in window 0); 0,2,3 zero.
	// Block 1: page 4 differs, page 5 zero, pages 6,7 match (2-page frame in
	// window 1).
	clear(srcData[0:pageSize])
	clear(srcData[2*pageSize : 4*pageSize])
	srcData[4*pageSize] ^= 0xFF
	clear(srcData[5*pageSize : 6*pageSize])

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	budget := DedupBudget{
		MaxFetchWindowsPerBlock:        1,
		MaxPromotedParentPagesPerBlock: 2,
		MaxPagesPerPromotedFrame:       1,
		FetchRunWindowPages:            4,
	}
	cache, meta, err := src.Dedup(t.Context(), &fakeOriginalDevice{data: baseData}, dirty, blockSize, t.TempDir()+"/dedup", false, false, budget)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 4, meta.Dirty.GetCardinality())
	for _, p := range []uint32{1, 4, 6, 7} {
		require.True(t, meta.Dirty.Contains(p), "page %d should be stored", p)
	}
	require.EqualValues(t, 4, meta.Empty.GetCardinality())

	// Dirty = {1,4,6,7} packs to slots 0-3.
	for slot, p := range []int64{1, 4, 6, 7} {
		got := make([]byte, pageSize)
		_, err := cache.ReadAt(got, int64(slot)*pageSize)
		require.NoError(t, err)
		require.Equal(t, srcData[p*pageSize:(p+1)*pageSize], got, "page %d", p)
	}
}

// A frame referenced by blocks outside the diff is fetched on restore no
// matter what this diff stores, so the global pass must not waste pages
// promoting it.
func TestCacheDedup_ExternallyReferencedFrameNotPromoted(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := 2 * blockSize

	baseData := make([]byte, size)
	_, err := rand.Read(baseData)
	require.NoError(t, err)
	srcData := make([]byte, size)
	copy(srcData, baseData)
	srcData[0] ^= 0xFF // page 0 differs; pages 1-3 match base

	build := uuid.New()
	hdr, err := header.NewHeader(
		header.NewTemplateMetadata(build, uint64(blockSize), uint64(size)),
		nil,
	)
	require.NoError(t, err)

	// Only block 0 is dirty; the 8-page fetch window covers the whole file,
	// so untouched block 1 keeps referencing the same parent frame.
	dirty := roaring.BitmapOf(0)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	cache, meta, err := src.Dedup(t.Context(), &fakeOriginalDevice{data: baseData, hdr: hdr}, dirty, blockSize, t.TempDir()+"/dedup", false, false, DedupBudget{MaxPagesPerPromotedFrame: 4, FetchRunWindowPages: 8})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 1, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(0))
}

// Parent pages must be keyed by the frames restore actually fetches: a build
// compressed with a non-default frame size uses its own frame table, not the
// configured window bucketing.
func TestParentFetchKey_UsesBuildFrameTable(t *testing.T) {
	t.Parallel()

	build := uuid.New()
	frameU := int32(1 << 20) // 1 MiB frames, half the default window
	ft := storage.NewFullFrameTable(storage.CompressionZstd, []storage.FrameSize{
		{U: frameU, C: 100}, {U: frameU, C: 100}, {U: frameU, C: 100},
	}).Table()

	hdr, err := header.NewHeader(header.NewTemplateMetadata(build, uint64(header.PageSize), 4<<20), nil)
	require.NoError(t, err)
	hdr.SetBuild(build, header.BuildData{FrameData: ft})

	windowBytes := int64(2 << 20)
	key := func(storageOff uint64) dedupFetchKey {
		return parentFetchKey(hdr, header.BuildMap{BuildId: build, Offset: storageOff}, true, 0, windowBytes)
	}

	// Offsets in frame 0 and frame 1 are distinct keys even though they share
	// one 2 MiB configured window; frame 1 and frame 2 differ as well.
	require.Equal(t, key(0), key(uint64(frameU)-1))
	require.NotEqual(t, key(0), key(uint64(frameU)))
	require.NotEqual(t, key(uint64(frameU)), key(uint64(2*frameU)))

	// Builds without frame data fall back to configured-window bucketing.
	other := uuid.New()
	fallback := func(storageOff uint64) dedupFetchKey {
		return parentFetchKey(hdr, header.BuildMap{BuildId: other, Offset: storageOff}, true, 0, windowBytes)
	}
	require.Equal(t, fallback(0), fallback(uint64(windowBytes)-1))
	require.NotEqual(t, fallback(0), fallback(uint64(windowBytes)))
}

func parentKeyedPage(buildID uuid.UUID, window int) dedupPageInfo {
	return dedupPageInfo{
		kind: dedupPageParent,
		key:  dedupFetchKey{buildID: buildID, window: window},
	}
}

func TestFetchWindowerCompact(t *testing.T) {
	t.Parallel()

	current := dedupPageInfo{kind: dedupPageCurrent}
	buildA, buildB, buildC := uuid.New(), uuid.New(), uuid.New()

	t.Run("zero-benefit promotion is not committed", func(t *testing.T) {
		t.Parallel()

		// Two parents in distinct windows; the budget only covers one, and
		// promoting it just trades a parent window for a current window.
		w := fetchWindower{windowPages: 4}
		pages := []dedupPageInfo{parentKeyedPage(buildA, 0), parentKeyedPage(buildA, 1)}
		require.Equal(t, 0, w.compact(pages, 1, 1))
		require.Equal(t, dedupPageParent, pages[0].kind)
	})

	t.Run("promotes a whole key group of non-adjacent parents", func(t *testing.T) {
		t.Parallel()

		// Two parents sharing one window, separated by a current page: the
		// whole key group is promoted together (2 -> 1 windows).
		w := fetchWindower{windowPages: 4}
		pages := []dedupPageInfo{parentKeyedPage(buildA, 0), current, parentKeyedPage(buildA, 0)}
		require.Equal(t, 2, w.compact(pages, 1, 4))
		require.Equal(t, dedupPageCurrent, pages[0].kind)
		require.Equal(t, dedupPageCurrent, pages[2].kind)
	})

	t.Run("combines distinct-key parents when only the union helps", func(t *testing.T) {
		t.Parallel()

		// current/A/current/B with two-page fetch windows: promoting either
		// parent alone opens a second current window (zero benefit), but
		// promoting both folds everything into two current windows (3 -> 2).
		w := fetchWindower{windowPages: 2}
		pages := []dedupPageInfo{current, parentKeyedPage(buildA, 0), current, parentKeyedPage(buildB, 0)}
		require.Equal(t, 2, w.compact(pages, 2, 2))
		require.Equal(t, dedupPageCurrent, pages[1].kind)
		require.Equal(t, dedupPageCurrent, pages[3].kind)
	})

	t.Run("budget spent on cheapest groups first", func(t *testing.T) {
		t.Parallel()

		// A spans two pages of one window; B and C are distinct single-page
		// windows. Budget 2 cannot eliminate A, but the cheaper B and C
		// collapse into one current window (3 -> 2 windows).
		w := fetchWindower{windowPages: 4}
		pages := []dedupPageInfo{
			parentKeyedPage(buildA, 0), parentKeyedPage(buildA, 0),
			parentKeyedPage(buildB, 0), parentKeyedPage(buildC, 0),
		}
		require.Equal(t, 2, w.compact(pages, 1, 2))
		require.Equal(t, dedupPageParent, pages[0].kind)
		require.Equal(t, dedupPageParent, pages[1].kind)
		require.Equal(t, dedupPageCurrent, pages[2].kind)
		require.Equal(t, dedupPageCurrent, pages[3].kind)
	})
}

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

func TestDedupPlanPromoteCheapestFrames(t *testing.T) {
	t.Parallel()

	buildA, buildB := uuid.New(), uuid.New()
	newPlan := func() *dedupPlan { return &dedupPlan{pageDirty: roaring.New(), pageEmpty: roaring.New()} }

	t.Run("cheapest keys first until budget", func(t *testing.T) {
		t.Parallel()

		p := newPlan()
		p.promoteCheapestFrames(map[dedupFetchKey]*roaring.Bitmap{
			{buildID: buildA, window: 0}: roaring.BitmapOf(10, 11, 12),
			{buildID: buildA, window: 1}: roaring.BitmapOf(20),
			{buildID: buildB, window: 0}: roaring.BitmapOf(30, 31),
		}, 3, 512)

		// The 1-page and 2-page keys fit the budget; the 3-page key does not.
		require.EqualValues(t, 2, p.promotedFrames)
		require.EqualValues(t, 3, p.promotedFramePages)
		require.True(t, p.pageDirty.Contains(20))
		require.True(t, p.pageDirty.Contains(30))
		require.True(t, p.pageDirty.Contains(31))
		require.False(t, p.pageDirty.Contains(10))
	})

	t.Run("full-window keys are never promoted", func(t *testing.T) {
		t.Parallel()

		p := newPlan()
		p.promoteCheapestFrames(map[dedupFetchKey]*roaring.Bitmap{
			{buildID: buildA, window: 0}: roaring.BitmapOf(0, 1, 2, 3),
		}, 100, 4)

		require.EqualValues(t, 0, p.promotedFrames)
		require.True(t, p.pageDirty.IsEmpty())
	})

	t.Run("nil map is a no-op", func(t *testing.T) {
		t.Parallel()

		p := newPlan()
		p.promoteCheapestFrames(nil, 100, 512)

		require.EqualValues(t, 0, p.promotedFrames)
	})
}

func TestCacheDedup_GlobalFrameBudgetPromotesCheapestKeys(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := 2 * blockSize

	baseData := make([]byte, size)
	_, err := rand.Read(baseData)
	require.NoError(t, err)
	srcData := make([]byte, size)
	copy(srcData, baseData)
	// Pages 0,4,6,7 differ (current); pages 1-3 match base in fetch window 0
	// (3 parent pages) and page 5 matches in window 1 (1 parent page).
	for _, p := range []int64{0, 4, 6, 7} {
		srcData[p*pageSize] ^= 0xFF
	}

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	// Budget 2 promotes only the cheapest key (window 1, 1 page); window 0
	// needs 3 more pages and does not fit.
	cache, meta, err := src.Dedup(t.Context(), &fakeOriginalDevice{data: baseData}, dirty, blockSize, t.TempDir()+"/dedup", false, false, DedupBudget{MaxPromotedParentPagesTotal: 2, FetchRunWindowPages: 4})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 5, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(5))
	for _, p := range []uint32{1, 2, 3} {
		require.False(t, meta.Dirty.Contains(p), "page %d should stay deduped", p)
	}
	require.EqualValues(t, 0, meta.Empty.GetCardinality())

	// The cache packs dirty pages: page 5 sits at slot Rank(5)-1.
	slot := int64(meta.Dirty.Rank(5)) - 1
	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, slot*pageSize)
	require.NoError(t, err)
	require.Equal(t, srcData[5*pageSize:6*pageSize], got)
}

// Exercises both budget passes together. The global budget (1 page) runs
// first and promotes the cheapest key (page 1); the per-block cap then sees
// the final layout, compacts block 0 by promoting page 3, and leaves block 1
// alone since it already fits — so page 5 stays deduped.
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
	// Block 0: pages 0,2 differ; parents 1,3 land in distinct 2-page windows.
	// Block 1: page 4 differs, page 5 matches, pages 6,7 are zero.
	for _, p := range []int64{0, 2, 4} {
		srcData[p*pageSize] ^= 0xFF
	}
	clear(srcData[6*pageSize : 8*pageSize])

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	budget := DedupBudget{
		MaxFetchWindowsPerBlock:        2,
		MaxPromotedParentPagesPerBlock: 2,
		MaxPromotedParentPagesTotal:    1,
		FetchRunWindowPages:            2,
	}
	cache, meta, err := src.Dedup(t.Context(), &fakeOriginalDevice{data: baseData}, dirty, blockSize, t.TempDir()+"/dedup", false, false, budget)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 5, meta.Dirty.GetCardinality())
	for _, p := range []uint32{0, 1, 2, 3, 4} {
		require.True(t, meta.Dirty.Contains(p), "page %d should be stored", p)
	}
	require.False(t, meta.Dirty.Contains(5), "page 5 should stay deduped")
	require.EqualValues(t, 2, meta.Empty.GetCardinality())

	// Dirty = {0..4}, so packed slot == page index for both promoted pages.
	for _, p := range []int64{1, 3} {
		got := make([]byte, pageSize)
		_, err := cache.ReadAt(got, p*pageSize)
		require.NoError(t, err)
		require.Equal(t, srcData[p*pageSize:(p+1)*pageSize], got, "promoted page %d", p)
	}
}

// With no promotion budget, parent pages that match the base stay deduped even
// when the block exceeds MaxFetchWindowsPerBlock: the compaction can't spend
// any promotions, so it must leave the parents out of the diff rather than
// over-promote them into Dirty.
func TestCacheDedup_BudgetExhaustionKeepsParentsDeduped(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize

	baseData := make([]byte, size)
	_, err := rand.Read(baseData)
	require.NoError(t, err)
	srcData := make([]byte, size)
	copy(srcData, baseData)
	// Pages 0 and 2 differ (current); pages 1 and 3 match base (parent).
	srcData[0] ^= 0xFF
	srcData[2*pageSize] ^= 0xFF

	dirty := fullDirty(size, blockSize)
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	// MaxFetchWindowsPerBlock unsatisfiable, but no promotions allowed.
	cache, meta, err := src.Dedup(t.Context(), &fakeOriginalDevice{data: baseData}, dirty, blockSize, t.TempDir()+"/dedup", false, false, DedupBudget{MaxFetchWindowsPerBlock: 0, MaxPromotedParentPagesPerBlock: 0, FetchRunWindowPages: 4})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	// Only the two genuinely differing pages are stored; matching parents are
	// deduped away (not promoted), and nothing is empty.
	require.EqualValues(t, 2, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(0))
	require.True(t, meta.Dirty.Contains(2))
	require.EqualValues(t, 0, meta.Empty.GetCardinality())
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

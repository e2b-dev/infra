//go:build linux

package block

import (
	"slices"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func parentPage() dedupPageInfo  { return dedupPageInfo{kind: dedupPageParent} }
func currentPage() dedupPageInfo { return dedupPageInfo{kind: dedupPageCurrent} }
func emptyPage() dedupPageInfo   { return dedupPageInfo{kind: dedupPageEmpty} }

func collectRuns(seq func(func([]int) bool)) [][]int {
	var runs [][]int
	for r := range seq {
		runs = append(runs, slices.Clone(r))
	}

	return runs
}

func TestParentRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		pages []dedupPageInfo
		want  [][]int
	}{
		{
			name:  "empty input",
			pages: nil,
			want:  nil,
		},
		{
			name:  "no parents",
			pages: []dedupPageInfo{currentPage(), emptyPage(), currentPage()},
			want:  nil,
		},
		{
			name:  "single parent",
			pages: []dedupPageInfo{parentPage()},
			want:  [][]int{{0}},
		},
		{
			name:  "contiguous parents are one run",
			pages: []dedupPageInfo{parentPage(), parentPage(), parentPage()},
			want:  [][]int{{0, 1, 2}},
		},
		{
			name:  "current page splits runs",
			pages: []dedupPageInfo{parentPage(), currentPage(), parentPage()},
			want:  [][]int{{0}, {2}},
		},
		{
			// Empty pages are virtual (never stored, no fetch window) so they
			// keep surrounding parents in the same run.
			name:  "empty page bridges parents into one run",
			pages: []dedupPageInfo{parentPage(), emptyPage(), parentPage()},
			want:  [][]int{{0, 2}},
		},
		{
			name:  "leading empty does not start a run",
			pages: []dedupPageInfo{emptyPage(), parentPage()},
			want:  [][]int{{1}},
		},
		{
			name:  "trailing empty is dropped from run",
			pages: []dedupPageInfo{parentPage(), emptyPage()},
			want:  [][]int{{0}},
		},
		{
			name: "mixed runs separated by current",
			pages: []dedupPageInfo{
				parentPage(), emptyPage(), parentPage(), // run {0,2}
				currentPage(),
				parentPage(), // run {4}
			},
			want: [][]int{{0, 2}, {4}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := collectRuns(parentRuns(tt.pages))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParentRuns_EarlyStopOnYieldFalse(t *testing.T) {
	t.Parallel()

	pages := []dedupPageInfo{
		parentPage(), currentPage(), parentPage(), currentPage(), parentPage(),
	}

	var seen [][]int
	for r := range parentRuns(pages) {
		seen = append(seen, slices.Clone(r))

		break // consumer stops after the first run
	}

	require.Equal(t, [][]int{{0}}, seen)
}

// compareBlock accumulates a block's results into the accumulator in place.
func TestCompareBlock_AccumulatesInPlace(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize

	// An all-zero source block makes every page classify as empty via the
	// header.IsZero fast path, so no base device read is needed.
	srcBlock := make([]byte, blockSize)

	cfg := dedupConfig{
		src:       func(int64) ([]byte, error) { return srcBlock, nil },
		blockSize: blockSize,
		budget:    DedupBudget{FetchRunWindowPages: 4},
	}

	acc := dedupAccum{
		pageDirty:    roaring.New(),
		pageEmpty:    roaring.New(),
		exportedSize: 7, // pre-existing value compareBlock must not touch
	}

	err := cfg.compareBlock(t.Context(), 0, &acc)
	require.NoError(t, err)

	// The four all-zero pages land in pageEmpty; nothing is stored as current.
	require.EqualValues(t, 4, acc.pageEmpty.GetCardinality())
	require.EqualValues(t, 0, acc.pageDirty.GetCardinality())
	require.EqualValues(t, 0, acc.currentStoredPages, "all-empty block stores no current pages")
	require.EqualValues(t, 0, acc.promotedBlocks)
	require.EqualValues(t, 7, acc.exportedSize, "compareBlock does not touch exportedSize")
}

// compareBlock accumulates across multiple calls into the same accumulator.
func TestCompareBlock_AccumulatesAcrossCalls(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	srcBlock := make([]byte, blockSize)

	cfg := dedupConfig{
		src:       func(int64) ([]byte, error) { return srcBlock, nil },
		blockSize: blockSize,
		budget:    DedupBudget{FetchRunWindowPages: 4},
	}

	acc := dedupAccum{pageDirty: roaring.New(), pageEmpty: roaring.New()}

	require.NoError(t, cfg.compareBlock(t.Context(), 0, &acc))
	require.NoError(t, cfg.compareBlock(t.Context(), blockSize, &acc))

	// Two all-zero blocks => 8 distinct empty page indices accumulated.
	require.EqualValues(t, 8, acc.pageEmpty.GetCardinality())
}

func TestParentKeyGroups(t *testing.T) {
	t.Parallel()

	keyA := dedupFetchKey{sourceType: parentFetchSource, window: 1}
	keyB := dedupFetchKey{sourceType: parentFetchSource, window: 2}

	pages := []dedupPageInfo{
		{kind: dedupPageParent, key: keyA},
		{kind: dedupPageCurrent},
		{kind: dedupPageParent, key: keyB},
		{kind: dedupPageParent, key: keyA},
		{kind: dedupPageEmpty},
	}

	// Groups are emitted ordered by their first page index, so the order is
	// stable across runs despite the underlying map. keyA's first index (0)
	// precedes keyB's (2).
	got := slices.Collect(parentKeyGroups(pages))
	require.Equal(t, [][]int{{0, 3}, {2}}, got)
}

// parentKeyPage returns a parent page whose fetch window is derived from
// byteOff, so count() groups same-window parents together.
func parentKeyPage(buildID uuid.UUID, byteOff int64, windowPages int) dedupPageInfo {
	return dedupPageInfo{
		kind: dedupPageParent,
		key: dedupFetchKey{
			sourceType: parentFetchSource,
			buildID:    buildID,
			window:     int(byteOff / (int64(windowPages) * header.PageSize)),
		},
	}
}

func TestFetchWindowerCount(t *testing.T) {
	t.Parallel()

	windowPages := 4
	build := uuid.New()
	w := fetchWindower{windowPages: windowPages, currentStart: 0}

	t.Run("empty pages contribute no windows", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, 0, w.count([]dedupPageInfo{emptyPage(), emptyPage()}))
	})

	t.Run("parents in same window collapse to one", func(t *testing.T) {
		t.Parallel()

		pages := []dedupPageInfo{
			parentKeyPage(build, 0, windowPages),
			parentKeyPage(build, header.PageSize, windowPages),
		}
		require.Equal(t, 1, w.count(pages))
	})

	t.Run("parents in different windows count separately", func(t *testing.T) {
		t.Parallel()

		pages := []dedupPageInfo{
			parentKeyPage(build, 0, windowPages),
			parentKeyPage(build, int64(windowPages)*header.PageSize, windowPages),
		}
		require.Equal(t, 2, w.count(pages))
	})
}

func TestFetchWindowerBestParentRun(t *testing.T) {
	t.Parallel()

	windowPages := 4
	build := uuid.New()

	t.Run("no parents yields nothing", func(t *testing.T) {
		t.Parallel()

		w := fetchWindower{windowPages: windowPages, currentStart: 0}
		pages := []dedupPageInfo{currentPage(), currentPage()}
		require.Nil(t, w.bestParentRun(pages, 4))
	})

	t.Run("budget too small to remove a window yields nothing", func(t *testing.T) {
		t.Parallel()

		w := fetchWindower{windowPages: windowPages, currentStart: 0}
		// Two parents in distinct windows; promoting one of them costs 1 but the
		// other still needs its window, and the promoted page joins a current
		// window, so a single promotion does not reduce the total.
		pages := []dedupPageInfo{
			parentKeyPage(build, 0, windowPages),
			parentKeyPage(build, int64(windowPages)*header.PageSize, windowPages),
		}
		require.Nil(t, w.bestParentRun(pages, 0))
	})

	t.Run("falls back to key grouping for non-adjacent parents", func(t *testing.T) {
		t.Parallel()

		w := fetchWindower{windowPages: windowPages, currentStart: 0}
		// Two parents sharing one window but separated by a current page: no
		// contiguous run covers both, so the key-group fallback selects them.
		sharedOff0 := int64(0)
		sharedOff1 := int64(header.PageSize)
		pages := []dedupPageInfo{
			parentKeyPage(build, sharedOff0, windowPages),
			currentPage(),
			parentKeyPage(build, sharedOff1, windowPages),
		}

		got := w.bestParentRun(pages, 4)
		require.Equal(t, []int{0, 2}, got)
	})
}

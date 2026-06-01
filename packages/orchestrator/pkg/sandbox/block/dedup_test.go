//go:build linux

package block

import (
	"bytes"
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

func TestRecordBlockPages(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)

	tests := []struct {
		name       string
		absOff     int64
		pages      []dedupPageInfo
		wantDirty  []uint32
		wantEmpty  []uint32
		wantStored int64
	}{
		{
			name:       "empty input writes nothing",
			absOff:     0,
			pages:      nil,
			wantDirty:  nil,
			wantEmpty:  nil,
			wantStored: 0,
		},
		{
			name:   "mixed kinds route to the right bitmap",
			absOff: 0,
			pages: []dedupPageInfo{
				emptyPage(),   // page 0 -> empty
				currentPage(), // page 1 -> dirty
				parentPage(),  // page 2 -> neither (deduped)
				currentPage(), // page 3 -> dirty
			},
			wantDirty:  []uint32{1, 3},
			wantEmpty:  []uint32{0},
			wantStored: 2,
		},
		{
			name:   "absOff offsets the page index",
			absOff: 4 * pageSize, // base page index 4
			pages: []dedupPageInfo{
				currentPage(), // -> index 4
				emptyPage(),   // -> index 5
			},
			wantDirty:  []uint32{4},
			wantEmpty:  []uint32{5},
			wantStored: 1,
		},
		{
			name:   "all parents store nothing",
			absOff: 0,
			pages:  []dedupPageInfo{parentPage(), parentPage()},
			// parents go into neither bitmap and are not counted.
			wantStored: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dirty := roaring.New()
			empty := roaring.New()

			stored := recordBlockPages(tt.absOff, tt.pages, dirty, empty)

			require.Equal(t, tt.wantStored, stored)
			require.ElementsMatch(t, tt.wantDirty, dirty.ToArray())
			require.ElementsMatch(t, tt.wantEmpty, empty.ToArray())
		})
	}
}

func TestFetchWindowerCompact(t *testing.T) {
	t.Parallel()

	windowPages := 4
	build := uuid.New()

	t.Run("non-positive maxWindows promotes nothing", func(t *testing.T) {
		t.Parallel()

		w := fetchWindower{windowPages: windowPages, currentStart: 0}
		pages := []dedupPageInfo{parentKeyPage(build, 0, windowPages)}
		require.Equal(t, 0, w.compact(pages, 0, 4))
		require.Equal(t, dedupPageParent, pages[0].kind, "page must stay parent")
	})

	t.Run("non-positive maxPromoted promotes nothing", func(t *testing.T) {
		t.Parallel()

		w := fetchWindower{windowPages: windowPages, currentStart: 0}
		pages := []dedupPageInfo{parentKeyPage(build, 0, windowPages)}
		require.Equal(t, 0, w.compact(pages, 1, 0))
		require.Equal(t, dedupPageParent, pages[0].kind)
	})

	t.Run("already within window budget promotes nothing", func(t *testing.T) {
		t.Parallel()

		w := fetchWindower{windowPages: windowPages, currentStart: 0}
		// A single parent window already satisfies maxWindows=1.
		pages := []dedupPageInfo{
			parentKeyPage(build, 0, windowPages),
			parentKeyPage(build, header.PageSize, windowPages),
		}
		require.Equal(t, 0, w.compact(pages, 1, 4))
		require.Equal(t, dedupPageParent, pages[0].kind)
		require.Equal(t, dedupPageParent, pages[1].kind)
	})

	t.Run("promotes a parent run to meet the window budget", func(t *testing.T) {
		t.Parallel()

		w := fetchWindower{windowPages: windowPages, currentStart: 0}
		// Two current pages in distinct windows plus one parent: two windows
		// total, over maxWindows=1. Promoting the lone parent removes its
		// parent window by folding it into a current window.
		pages := []dedupPageInfo{
			currentPage(),
			parentKeyPage(build, 0, windowPages),
			currentPage(),
		}

		promoted := w.compact(pages, 1, 4)
		require.Positive(t, promoted)
		require.Equal(t, dedupPageCurrent, pages[1].kind, "parent must be promoted")
		require.Equal(t, dedupFetchKey{}, pages[1].key, "promoted key is cleared")
	})
}

func TestFetchWindowerCountAfter(t *testing.T) {
	t.Parallel()

	windowPages := 4
	build := uuid.New()
	w := fetchWindower{windowPages: windowPages, currentStart: 0}

	// One parent window plus one current window => two windows.
	pages := []dedupPageInfo{
		parentKeyPage(build, 0, windowPages),
		currentPage(),
	}
	require.Equal(t, 2, w.count(pages))

	// Promoting the parent folds it into the current window: one window left.
	require.Equal(t, 1, w.countAfter(pages, []int{0}))

	// countAfter must not mutate the input slice.
	require.Equal(t, dedupPageParent, pages[0].kind, "input page kind unchanged")
	require.Equal(t,
		dedupFetchKey{sourceType: parentFetchSource, buildID: build, window: 0},
		pages[0].key,
		"input page key unchanged",
	)
}

func TestFetchWindowerBestByRatio(t *testing.T) {
	t.Parallel()

	windowPages := 4
	build := uuid.New()
	w := fetchWindower{windowPages: windowPages, currentStart: 0}

	// current, parent (window 0), current: the parent is its own fetch window,
	// so promoting it into the surrounding current window removes one window.
	pages := []dedupPageInfo{
		currentPage(),
		parentKeyPage(build, 0, windowPages),
		currentPage(),
	}
	before := w.count(pages)
	require.Equal(t, 2, before)

	t.Run("candidate over budget is skipped", func(t *testing.T) {
		t.Parallel()

		got := w.bestByRatio(pages, 0, before, slices.Values([][]int{{1}}))
		require.Nil(t, got, "cost 1 exceeds budget 0")
	})

	t.Run("beneficial candidate within budget is selected", func(t *testing.T) {
		t.Parallel()

		got := w.bestByRatio(pages, windowPages, before, slices.Values([][]int{{1}}))
		require.Equal(t, []int{1}, got)
	})

	t.Run("zero-benefit candidate is skipped", func(t *testing.T) {
		t.Parallel()

		// Index 0 is already current; promoting it removes no fetch window.
		got := w.bestByRatio(pages, windowPages, before, slices.Values([][]int{{0}}))
		require.Nil(t, got)
	})

	t.Run("over-budget run is clamped to an affordable, beneficial prefix", func(t *testing.T) {
		t.Parallel()

		// Three parent pages in three distinct single-page windows: the whole
		// run costs 3 but budget is 2. Clamping to the first two folds them into
		// one current window, leaving one parent window (3 -> 2 windows).
		run := []dedupPageInfo{
			parentKeyPage(build, 0, windowPages),
			parentKeyPage(build, int64(windowPages)*header.PageSize, windowPages),
			parentKeyPage(build, 2*int64(windowPages)*header.PageSize, windowPages),
		}
		got := w.bestByRatio(run, 2, w.count(run), slices.Values([][]int{{0, 1, 2}}))
		require.Equal(t, []int{0, 1}, got)
	})
}

// classifyPage maps a source page to empty/parent/current based on the parent
// header mapping, the cache-peek (best-effort) check, and the base bytes.
func TestClassifyPage(t *testing.T) {
	t.Parallel()

	const windowPages = 4
	pageSize := int64(header.PageSize)
	build := uuid.New()

	zeroPage := make([]byte, header.PageSize)
	nonZero := bytes.Repeat([]byte{0xAB}, header.PageSize)
	budget := DedupBudget{FetchRunWindowPages: windowPages}

	t.Run("zero source page is empty without reading base", func(t *testing.T) {
		t.Parallel()

		base := &fakeOriginalDevice{data: make([]byte, pageSize)}
		cfg := dedupConfig{base: base, baseHeader: base.Header(), blockSize: pageSize, budget: budget}

		info, err := cfg.classifyPage(t.Context(), zeroPage, 0)
		require.NoError(t, err)
		require.Equal(t, dedupPageEmpty, info.kind)
		require.Zero(t, base.reads, "zero fast path must not read base")
	})

	t.Run("nil-build parent hole is current without reading base", func(t *testing.T) {
		t.Parallel()

		hdr, err := header.NewHeader(
			header.NewTemplateMetadata(uuid.Nil, uint64(pageSize), uint64(pageSize)),
			[]header.BuildMap{{Offset: 0, Length: uint64(pageSize), BuildId: uuid.Nil}},
		)
		require.NoError(t, err)
		base := &fakeOriginalDevice{data: bytes.Repeat([]byte{0xFF}, int(pageSize)), hdr: hdr}
		cfg := dedupConfig{base: base, baseHeader: hdr, blockSize: pageSize, budget: budget}

		info, err := cfg.classifyPage(t.Context(), nonZero, 0)
		require.NoError(t, err)
		require.Equal(t, dedupPageCurrent, info.kind)
		require.Zero(t, base.reads, "nil-build hole must not read base")
	})

	t.Run("page matching mapped parent is keyed by build and storage window", func(t *testing.T) {
		t.Parallel()

		hdr, err := header.NewHeader(
			header.NewTemplateMetadata(build, uint64(pageSize), uint64(pageSize)),
			[]header.BuildMap{{Offset: 0, Length: uint64(pageSize), BuildId: build}},
		)
		require.NoError(t, err)
		base := &fakeOriginalDevice{data: nonZero, hdr: hdr}
		cfg := dedupConfig{base: base, baseHeader: hdr, blockSize: pageSize, budget: budget}

		info, err := cfg.classifyPage(t.Context(), nonZero, 0)
		require.NoError(t, err)
		require.Equal(t, dedupPageParent, info.kind)
		require.Equal(t,
			dedupFetchKey{sourceType: parentFetchSource, buildID: build, window: 0},
			info.key,
		)
		require.Equal(t, 1, base.reads, "matching path reads base once")
	})

	t.Run("page matching unmapped parent is keyed by offset window", func(t *testing.T) {
		t.Parallel()

		pageOff := int64(8) * pageSize
		data := make([]byte, 9*pageSize)
		copy(data[pageOff:], nonZero)
		base := &fakeOriginalDevice{data: data} // nil header -> no mapping
		cfg := dedupConfig{base: base, baseHeader: nil, blockSize: pageSize, budget: budget}

		info, err := cfg.classifyPage(t.Context(), nonZero, pageOff)
		require.NoError(t, err)
		require.Equal(t, dedupPageParent, info.kind)
		require.Equal(t,
			dedupFetchKey{
				sourceType: parentFetchSource,
				window:     int(pageOff / (int64(windowPages) * pageSize)),
			},
			info.key,
		)
	})

	t.Run("page differing from base is current", func(t *testing.T) {
		t.Parallel()

		base := &fakeOriginalDevice{data: bytes.Repeat([]byte{0x11}, int(pageSize))}
		cfg := dedupConfig{base: base, baseHeader: nil, blockSize: pageSize, budget: budget}

		info, err := cfg.classifyPage(t.Context(), nonZero, 0)
		require.NoError(t, err)
		require.Equal(t, dedupPageCurrent, info.kind)
		require.Equal(t, 1, base.reads, "diff path reads base once")
	})

	t.Run("best-effort uncached page is current without reading base", func(t *testing.T) {
		t.Parallel()

		base := &peekingOriginalDevice{fakeOriginalDevice: fakeOriginalDevice{data: nonZero}, cached: false}
		cfg := dedupConfig{base: base, baseHeader: nil, peeker: base, blockSize: pageSize, bestEffort: true, budget: budget}

		info, err := cfg.classifyPage(t.Context(), nonZero, 0)
		require.NoError(t, err)
		require.Equal(t, dedupPageCurrent, info.kind)
		require.Zero(t, base.reads, "uncached best-effort page must not read base")
	})
}

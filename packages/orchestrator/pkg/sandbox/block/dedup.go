//go:build linux

package block

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type dedupPlan struct {
	pageDirty      *roaring.Bitmap
	pageEmpty      *roaring.Bitmap
	exportedSize   int64
	promotedBlocks int64
	promotedPages  int64
}

type DedupBudget struct {
	MaxFetchWindowsPerBlock        int
	MaxPromotedParentPagesPerBlock int
	FetchRunWindowPages            int
}

type dedupPageKind byte

const (
	dedupPageEmpty dedupPageKind = iota
	dedupPageParent
	dedupPageCurrent
)

type fetchSource byte

const (
	currentFetchSource fetchSource = iota + 1
	parentFetchSource
)

const (
	defaultDedupFetchWindowPages = storage.DefaultCompressFrameSize / header.PageSize
)

type dedupFetchKey struct {
	sourceType fetchSource
	buildID    uuid.UUID
	window     int
}

type dedupPageInfo struct {
	kind dedupPageKind
	key  dedupFetchKey
}

// blockReader returns the source bytes for the block at absOff.
type blockReader func(absOff int64) ([]byte, error)

// dedupConfig holds the immutable inputs of a dedup comparison. Its methods
// only read it; all mutable accumulation lives in dedupAccum.
type dedupConfig struct {
	src        blockReader
	base       ReadonlyDevice
	baseHeader *header.Header
	peeker     CachePeeker

	blockSize  int64
	bestEffort bool
	budget     DedupBudget
}

// dedupAccum is the mutable running state of a dedup comparison. compareBlock
// accumulates each block's results into it in place.
type dedupAccum struct {
	pageDirty          *roaring.Bitmap
	pageEmpty          *roaring.Bitmap
	exportedSize       int64
	promotedBlocks     int64
	promotedPages      int64
	currentStoredPages int64
}

// dedupCompare classifies each dirty page against base into pageDirty or
// pageEmpty. Per-page IsCached so a single uncached neighbour can't poison
// cached pages of the same block when the parent header is page-granular.
func dedupCompare(
	ctx context.Context,
	src blockReader,
	base ReadonlyDevice,
	dirty *roaring.Bitmap,
	blockSize int64,
	bestEffort bool,
	budget DedupBudget,
) (*dedupPlan, error) {
	if budget.FetchRunWindowPages <= 0 {
		budget.FetchRunWindowPages = defaultDedupFetchWindowPages
	}

	peeker, _ := base.(CachePeeker)
	cfg := dedupConfig{
		src:        src,
		base:       base,
		baseHeader: base.Header(),
		peeker:     peeker,
		blockSize:  blockSize,
		bestEffort: bestEffort,
		budget:     budget,
	}
	acc := dedupAccum{
		pageDirty: roaring.New(),
		pageEmpty: roaring.New(),
	}

	for r := range BitsetRanges(dirty, blockSize) {
		acc.exportedSize += r.Size

		for chunkOff := int64(0); chunkOff < r.Size; chunkOff += blockSize {
			if err := cfg.compareBlock(ctx, r.Start+chunkOff, &acc); err != nil {
				return nil, err
			}
		}
	}

	return &dedupPlan{
		pageDirty:      acc.pageDirty,
		pageEmpty:      acc.pageEmpty,
		exportedSize:   acc.exportedSize,
		promotedBlocks: acc.promotedBlocks,
		promotedPages:  acc.promotedPages,
	}, nil
}

// compareBlock classifies one block and accumulates its results into acc,
// mutating it in place.
func (c dedupConfig) compareBlock(ctx context.Context, absOff int64, acc *dedupAccum) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	srcBuf, err := c.src(absOff)
	if err != nil {
		return err
	}

	pagesPerBlock := int(c.blockSize / header.PageSize)
	blockPages := make([]dedupPageInfo, pagesPerBlock)

	for page := range pagesPerBlock {
		pageStart := int64(page) * header.PageSize
		srcPage := srcBuf[pageStart : pageStart+header.PageSize]

		info, err := c.classifyPage(ctx, srcPage, absOff+pageStart)
		if err != nil {
			return err
		}

		blockPages[page] = info
	}

	promoted := c.promoteBlockPages(blockPages, acc.currentStoredPages)
	if promoted > 0 {
		acc.promotedBlocks++
		acc.promotedPages += int64(promoted)
	}

	acc.currentStoredPages += recordBlockPages(absOff, blockPages, acc.pageDirty, acc.pageEmpty)

	return nil
}

func (c dedupConfig) classifyPage(ctx context.Context, srcPage []byte, pageOff int64) (dedupPageInfo, error) {
	if header.IsZero(srcPage) {
		return dedupPageInfo{}, nil
	}

	mapped, err := c.baseHeader.GetShiftedMapping(ctx, pageOff)
	hasMapping := err == nil

	if hasMapping && mapped.BuildId == uuid.Nil && int64(mapped.Length) >= header.PageSize {
		return dedupPageInfo{kind: dedupPageCurrent}, nil
	}

	if c.skipUncachedPage(ctx, pageOff) {
		return dedupPageInfo{kind: dedupPageCurrent}, nil
	}

	basePage, err := c.base.Slice(ctx, pageOff, header.PageSize)
	if err != nil {
		return dedupPageInfo{}, fmt.Errorf("slice base at %d: %w", pageOff, err)
	}

	if !bytes.Equal(srcPage, basePage) {
		return dedupPageInfo{kind: dedupPageCurrent}, nil
	}

	windowBytes := c.budget.FetchRunWindowPages * header.PageSize
	key := dedupFetchKey{sourceType: parentFetchSource}
	if hasMapping {
		key.buildID = mapped.BuildId
		key.window = int(mapped.Offset / uint64(windowBytes))
	} else {
		key.window = int(pageOff / int64(windowBytes))
	}

	return dedupPageInfo{kind: dedupPageParent, key: key}, nil
}

func (c dedupConfig) skipUncachedPage(ctx context.Context, pageOff int64) bool {
	return c.bestEffort && c.peeker != nil && !c.peeker.IsCached(ctx, pageOff, header.PageSize)
}

func (c dedupConfig) promoteBlockPages(blockPages []dedupPageInfo, currentStoredPages int64) int {
	w := fetchWindower{
		windowPages:  c.budget.FetchRunWindowPages,
		currentStart: currentStoredPages,
	}

	return w.compact(blockPages, c.budget.MaxFetchWindowsPerBlock, c.budget.MaxPromotedParentPagesPerBlock)
}

// recordBlockPages writes this block's classified pages into the diff bitmaps
// and returns how many current pages were stored.
func recordBlockPages(absOff int64, blockPages []dedupPageInfo, pageDirty, pageEmpty *roaring.Bitmap) int64 {
	var storedPages int64
	for page, info := range blockPages {
		pageIdx := uint32(absOff/header.PageSize) + uint32(page)
		switch info.kind {
		case dedupPageEmpty:
			pageEmpty.Add(pageIdx)
		case dedupPageCurrent:
			pageDirty.Add(pageIdx)
			storedPages++
		}
	}

	return storedPages
}

// fetchWindower groups pages into fetch-run windows. windowPages and
// currentStart are invariant for the lifetime of a compact pass, so they live
// on the receiver instead of being threaded through every call.
type fetchWindower struct {
	windowPages  int
	currentStart int64
}

// compact promotes parent pages to current until the block fits within
// maxWindows fetch windows or the promotion budget is exhausted.
func (w fetchWindower) compact(pages []dedupPageInfo, maxWindows, maxPromoted int) int {
	if maxWindows <= 0 || maxPromoted <= 0 {
		return 0
	}

	var promoted int
	for promoted < maxPromoted && w.count(pages) > maxWindows {
		idxs := w.bestParentRun(pages, maxPromoted-promoted)
		if len(idxs) == 0 {
			break
		}
		for _, i := range idxs {
			pages[i].kind = dedupPageCurrent
			pages[i].key = dedupFetchKey{}
			promoted++
		}
	}

	return promoted
}

func (w fetchWindower) count(pages []dedupPageInfo) int {
	keys := make(map[dedupFetchKey]struct{})
	var currentOrdinal int64
	for _, p := range pages {
		switch p.kind {
		case dedupPageParent:
			keys[p.key] = struct{}{}
		case dedupPageCurrent:
			keys[dedupFetchKey{
				sourceType: currentFetchSource,
				window:     int(w.currentStart+currentOrdinal) / w.windowPages,
			}] = struct{}{}
			currentOrdinal++
		}
	}

	return len(keys)
}

// bestParentRun returns the parent page indices whose promotion to current best
// reduces fetch windows per promoted page, scanning contiguous parent/empty
// runs first and falling back to per-key promotion.
func (w fetchWindower) bestParentRun(pages []dedupPageInfo, budget int) []int {
	before := w.count(pages)
	if best := w.bestByRatio(pages, budget, before, parentRuns(pages)); best != nil {
		return best
	}
	if best := w.bestByRatio(pages, budget, before, parentKeyGroups(pages)); best != nil {
		return best
	}

	// Last resort: promote parents across fetch keys together. Some blocks
	// (e.g. current/A/current/B) only shrink when distinct-key parents are
	// promoted jointly, since each alone just opens another current window.
	return w.bestByRatio(pages, budget, before, parentUnion(pages))
}

// bestByRatio picks the candidate page set whose promotion removes the most
// fetch windows per promoted page (highest benefit/cost), within budget.
func (w fetchWindower) bestByRatio(pages []dedupPageInfo, budget, before int, candidates iter.Seq[[]int]) []int {
	var best []int
	bestBenefit, bestCost := 0, 0
	consider := func(idxs []int) {
		cost := len(idxs)
		if cost == 0 || cost > budget {
			return
		}
		benefit := before - w.countAfter(pages, idxs)
		if benefit <= 0 {
			return
		}
		if best == nil || cost*bestBenefit < bestCost*benefit {
			best, bestBenefit, bestCost = idxs, benefit, cost
		}
	}
	for idxs := range candidates {
		if len(idxs) <= budget {
			consider(idxs)

			continue
		}
		// An over-budget candidate can still help via an affordable sub-slice.
		// Slide a budget-sized window across it so a beneficial tail (not just
		// the prefix) is considered; the benefit check discards sub-slices that
		// don't remove a window.
		for start := 0; start+budget <= len(idxs); start++ {
			consider(idxs[start : start+budget])
		}
	}

	return best
}

// parentRuns yields the parent page indices of each maximal run of parent/empty
// pages. A run starts at a parent page and extends across adjacent parent and
// empty pages; current pages and the slice ends bound it.
func parentRuns(pages []dedupPageInfo) iter.Seq[[]int] {
	return func(yield func([]int) bool) {
		var run []int
		for i, p := range pages {
			switch p.kind {
			case dedupPageParent:
				run = append(run, i)
			case dedupPageEmpty:
				// Empties extend an in-progress run but never start one.
			default: // dedupPageCurrent
				if len(run) > 0 && !yield(run) {
					return
				}
				run = nil
			}
		}
		if len(run) > 0 {
			yield(run)
		}
	}
}

// parentKeyGroups yields the parent page indices grouped by fetch key, so a set
// of non-adjacent parents sharing one fetch window can be promoted together.
// Groups are ordered by their first page index so the selection is
// deterministic regardless of map iteration order.
func parentKeyGroups(pages []dedupPageInfo) iter.Seq[[]int] {
	idxByKey := make(map[dedupFetchKey][]int)
	for i, p := range pages {
		if p.kind == dedupPageParent {
			idxByKey[p.key] = append(idxByKey[p.key], i)
		}
	}

	groups := slices.Collect(maps.Values(idxByKey))
	slices.SortFunc(groups, func(a, b []int) int {
		return a[0] - b[0]
	})

	return slices.Values(groups)
}

// parentUnion yields all parent page indices as a single candidate so parents
// in different fetch windows can be promoted together when only their combined
// promotion removes a fetch window.
func parentUnion(pages []dedupPageInfo) iter.Seq[[]int] {
	return func(yield func([]int) bool) {
		var all []int
		for i, p := range pages {
			if p.kind == dedupPageParent {
				all = append(all, i)
			}
		}
		if len(all) > 0 {
			yield(all)
		}
	}
}

// countAfter counts fetch windows as if the given parent indices were promoted
// to current, without mutating pages.
func (w fetchWindower) countAfter(pages []dedupPageInfo, promote []int) int {
	candidate := slices.Clone(pages)
	for _, i := range promote {
		candidate[i].kind = dedupPageCurrent
		candidate[i].key = dedupFetchKey{}
	}

	return w.count(candidate)
}

// dedupDrain writes pageDirty pages from src to outPath packed at PageSize.
func dedupDrain(
	ctx context.Context,
	src blockReader,
	pageDirty *roaring.Bitmap,
	blockSize int64,
	outPath string,
	directIO bool,
) (*Cache, error) {
	openFlags := os.O_RDWR | os.O_CREATE
	if directIO {
		openFlags |= unix.O_DIRECT
	}
	f, err := os.OpenFile(outPath, openFlags, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open dedup cache: %w", err)
	}
	if want := int64(pageDirty.GetCardinality()) * header.PageSize; directIO && want > 0 {
		if fErr := unix.Fallocate(int(f.Fd()), 0, 0, want); fErr != nil {
			logger.L().Warn(ctx, "fallocate dedup cache; proceeding without preallocation", zap.Error(fErr))
		}
	}

	fileOff, err := drainDirtyPages(ctx, int(f.Fd()), src, pageDirty, blockSize)
	if err != nil {
		return nil, errors.Join(err, f.Close(), os.Remove(outPath))
	}

	if directIO {
		if err := f.Truncate(fileOff); err != nil {
			return nil, errors.Join(fmt.Errorf("truncate dedup cache: %w", err), f.Close(), os.Remove(outPath))
		}
	}
	if err := f.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(outPath))
	}

	cache, err := NewCache(fileOff, header.PageSize, outPath, false)
	if err != nil {
		return nil, errors.Join(err, os.Remove(outPath))
	}
	cache.setIsCached(0, fileOff)

	return cache, nil
}

func recordDedupAttrs(ctx context.Context, totalPages, uniquePages, emptyPages, promotedBlocks, promotedPages int64, compareDur, writeDur time.Duration) {
	dedupedPages := totalPages - uniquePages - emptyPages
	ratio := 0.0
	if totalPages > 0 {
		ratio = float64(dedupedPages) / float64(totalPages)
	}
	telemetry.SetAttributes(ctx,
		attribute.Int64("dedup.total_pages", totalPages),
		attribute.Int64("dedup.deduped_pages", dedupedPages),
		attribute.Int64("dedup.unique_pages", uniquePages),
		attribute.Int64("dedup.empty_pages", emptyPages),
		attribute.Int64("dedup.promoted_blocks", promotedBlocks),
		attribute.Int64("dedup.promoted_pages", promotedPages),
		attribute.Float64("dedup.ratio", ratio),
		attribute.Int64("dedup.compare_ms", compareDur.Milliseconds()),
		attribute.Int64("dedup.write_ms", writeDur.Milliseconds()),
	)
}

// drainDirtyPages packs pageDirty pages from src into fd. Mirrors
// Cache.copyProcessMemory: coalesce contiguous pages into ranges, carve at
// source-block boundaries, pre-split over MAX_RW_COUNT, then drainIovs.
func drainDirtyPages(ctx context.Context, fd int, src blockReader, pageDirty *roaring.Bitmap, blockSize int64) (int64, error) {
	var ranges []Range
	for r := range BitsetRanges(pageDirty, header.PageSize) {
		for off := r.Start; off < r.End(); {
			blockOff := (off / blockSize) * blockSize
			chunkEnd := min(r.End(), blockOff+blockSize)
			ranges = append(ranges, Range{Start: off, Size: chunkEnd - off})
			off = chunkEnd
		}
	}
	ranges = splitOversizedRanges(ranges, getAlignedMaxRwCount(header.PageSize))

	if err := drainIovs(ranges, func(r Range) int64 { return r.Size }, header.PageSize,
		func(destOff int64, batch []Range, _ int64) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			iovs := make([][]byte, len(batch))
			for i, r := range batch {
				blockOff := (r.Start / blockSize) * blockSize
				buf, srcErr := src(blockOff)
				if srcErr != nil {
					return fmt.Errorf("slice src at %d: %w", blockOff, srcErr)
				}
				iovs[i] = buf[r.Start-blockOff : r.Start-blockOff+r.Size]
			}
			if err := pwritevAll(fd, destOff, iovs); err != nil {
				return fmt.Errorf("pwritev dedup pages: %w", err)
			}

			return nil
		}); err != nil {
		return 0, err
	}

	return GetSize(ranges), nil
}

// Dedup writes pages from c that differ from base, packed at PageSize, to
// outPath. bestEffort skips uncached blocks; directIO uses O_DIRECT.
func (c *Cache) Dedup(
	ctx context.Context,
	base ReadonlyDevice,
	dirty *roaring.Bitmap,
	blockSize int64,
	outPath string,
	bestEffort bool,
	directIO bool,
	budget DedupBudget,
) (*Cache, *header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "dedup-pages")
	defer span.End()

	// c is packed in BitsetRanges order; map abs offset → packed offset.
	packed := make(map[int64]int64, dirty.GetCardinality())
	var cum int64
	for r := range BitsetRanges(dirty, blockSize) {
		for chunkOff := int64(0); chunkOff < r.Size; chunkOff += blockSize {
			packed[r.Start+chunkOff] = cum
			cum += blockSize
		}
	}
	src := func(absOff int64) ([]byte, error) {
		idx, ok := packed[absOff]
		if !ok {
			return nil, fmt.Errorf("dedup src: %d not packed", absOff)
		}

		return c.Slice(idx, blockSize)
	}

	compareStart := time.Now()
	plan, err := dedupCompare(ctx, src, base, dirty, blockSize, bestEffort, budget)
	if err != nil {
		return nil, nil, err
	}
	compareDur := time.Since(compareStart)

	writeStart := time.Now()
	cache, err := dedupDrain(ctx, src, plan.pageDirty, blockSize, outPath, directIO)
	if err != nil {
		return nil, nil, err
	}
	recordDedupAttrs(ctx,
		plan.exportedSize/header.PageSize,
		int64(plan.pageDirty.GetCardinality()),
		int64(plan.pageEmpty.GetCardinality()),
		plan.promotedBlocks,
		plan.promotedPages,
		compareDur, time.Since(writeStart),
	)

	return cache, &header.DiffMetadata{
		Dirty:     plan.pageDirty,
		Empty:     plan.pageEmpty,
		BlockSize: header.PageSize,
	}, nil
}

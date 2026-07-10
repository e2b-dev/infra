//go:build linux

package prefetch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/units"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/prefetch")

type prefetchData struct {
	offset int64
	data   []byte
}

// extent is a run of contiguous block indices fetched from the source in a
// single source.Slice call. Coalescing contiguous indices into one extent
// turns many small sequential reads into fewer, larger ones. The copy phase
// always stays per-block (split out of the fetched extent), since UFFDIO_COPY
// requires page-sized data.
type extent struct {
	startIdx uint64
	blocks   int
}

// coalesceIndices groups sorted, deduplicated block indices into maximal
// contiguous runs, each capped at maxBlocks. With maxBlocks<=1 every extent is
// a single block, reproducing the per-block fetch exactly (coalescing off).
func coalesceIndices(indices []uint64, maxBlocks int) []extent {
	if maxBlocks < 1 {
		maxBlocks = 1
	}

	out := make([]extent, 0, len(indices))
	for i := 0; i < len(indices); {
		n := 1
		for i+n < len(indices) && indices[i+n] == indices[i+n-1]+1 && n < maxBlocks {
			n++
		}

		out = append(out, extent{startIdx: indices[i], blocks: n})
		i += n
	}

	return out
}

// Prefetcher handles background prefetching of memory pages.
// It proactively fetches pages that are known to be needed based on the prefetch mapping
// collected during template build.
//
// The prefetcher works in two parallel phases:
// 1. Fetch phase: Immediately starts fetching pages from the source (populates cache)
// 2. Copy phase: Once uffd is ready, copies the fetched pages to guest memory
//
// Both phases run with their own parallelism limits and don't block each other.
type Prefetcher struct {
	logger       logger.Logger
	source       block.Slicer
	uffd         uffd.MemoryBackend
	mapping      *metadata.MemoryPrefetchMapping
	featureFlags *featureflags.Client
}

func New(
	logger logger.Logger,
	source block.Slicer,
	uffd uffd.MemoryBackend,
	mapping *metadata.MemoryPrefetchMapping,
	featureFlags *featureflags.Client,
) *Prefetcher {
	return &Prefetcher{
		logger:       logger,
		source:       source,
		uffd:         uffd,
		mapping:      mapping,
		featureFlags: featureFlags,
	}
}

// Start begins the background prefetching process.
// This is fire-and-forget - errors are logged but don't affect sandbox operation.
// The prefetcher will stop when the context is cancelled.
//
// The prefetcher starts fetching pages from the source immediately (to populate the cache),
// while simultaneously waiting for the uffd handler. Once the handler is ready, it starts
// copying the fetched pages to guest memory.
func (p *Prefetcher) Start(ctx context.Context) error {
	ctx = storage.WithSkipCacheWriteback(ctx)

	ctx, span := tracer.Start(ctx, "start prefetch")
	defer span.End()

	if p.mapping == nil {
		p.logger.Debug(ctx, "prefetch: no mapping provided, skipping")

		return nil
	}

	indices := p.mapping.Indices
	if len(indices) == 0 {
		p.logger.Debug(ctx, "prefetch: no pages to prefetch")

		return nil
	}

	// Get worker counts and coalescing config from feature flags at runtime
	maxFetchWorkers := p.featureFlags.IntFlag(ctx, featureflags.MemoryPrefetchMaxFetchWorkers)
	maxCopyWorkers := p.featureFlags.IntFlag(ctx, featureflags.MemoryPrefetchMaxCopyWorkers)
	coalesceMaxMB := p.featureFlags.IntFlag(ctx, featureflags.MemoryPrefetchCoalesceMaxMB)

	// cancelRun aborts the whole run early. Copy workers fire it on ErrClosed
	// (uffd gone: sandbox teardown) so fetch workers stop fetching and
	// queueing pages nobody will ever copy, instead of running the fetch
	// phase to completion for a dead sandbox.
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	blockSize := p.mapping.BlockSize
	totalPages := len(indices)

	span.SetAttributes(
		attribute.Int64("prefetch.total_pages", int64(totalPages)),
		attribute.Int64("prefetch.block_size", blockSize),
		attribute.Int("prefetch.max_fetch_workers", maxFetchWorkers),
		attribute.Int("prefetch.max_copy_workers", maxCopyWorkers),
		attribute.Int("prefetch.coalesce_max_mb", coalesceMaxMB),
	)

	p.logger.Debug(ctx, "prefetch: starting background prefetch",
		zap.Int("total_pages", totalPages),
		zap.Int64("block_size", blockSize),
		zap.Int("max_fetch_workers", maxFetchWorkers),
		zap.Int("max_copy_workers", maxCopyWorkers),
		zap.Int("coalesce_max_mb", coalesceMaxMB),
	)

	// Coalesce contiguous blocks into extents. maxBlk<=1 (coalesceMaxMB<=0,
	// the default) reproduces the per-block fetch exactly.
	maxBlk := 1
	if coalesceMaxMB > 0 && blockSize > 0 {
		maxBlk = max(1, int(units.MBToBytes(int64(coalesceMaxMB))/blockSize))
	}
	extents := coalesceIndices(indices, maxBlk)
	span.SetAttributes(attribute.Int("prefetch.extents", len(extents)))

	// Channels for work distribution
	// Fetch channel: all extents to fetch (large buffer so main goroutine doesn't block)
	fetchCh := make(chan extent, len(extents))
	// Copy channel: offsets ready to copy (fetched successfully)
	copyCh := make(chan prefetchData, totalPages)

	// Counters for statistics
	var fetchedCount atomic.Uint64
	var copiedCount atomic.Uint64
	var fetchSkippedCount atomic.Uint64
	var copySkippedCount atomic.Uint64

	var fetchWg sync.WaitGroup
	var copyWg sync.WaitGroup

	runStart := time.Now()
	// Set by the copy coordinator once uffd is ready; stays nil if the
	// context is cancelled before the copy phase ever starts. Holds a
	// *time.Time (not UnixNano) so the monotonic reading survives and the
	// duration below is immune to wall-clock steps.
	var copyStart atomic.Pointer[time.Time]

	// Queue all extents to fetch, in offset order.
	for _, e := range extents {
		fetchCh <- e
	}
	close(fetchCh)

	// Start fetch workers - they populate the cache and queue offsets for copy
	for range maxFetchWorkers {
		fetchWg.Go(func() {
			p.fetchWorker(ctx, fetchCh, copyCh, blockSize, &fetchedCount, &fetchSkippedCount)
		})
	}

	// Start copy coordinator - waits for uffd ready, then spawns copy workers
	copyWg.Go(func() {
		p.startCopyWorkers(ctx, cancelRun, copyCh, maxCopyWorkers, &copyStart, &copiedCount, &copySkippedCount)
	})

	// Wait for fetch workers to complete
	fetchWg.Wait()
	fetchDuration := time.Since(runStart)
	// Close copy channel when all fetch workers are done
	close(copyCh)

	// Wait for copy workers to complete
	copyWg.Wait()

	// Export the run stats: per-stage page counts plus phase durations. The
	// fetch and copy phases overlap; "total" spans the whole run.
	pagesCounter.Add(ctx, int64(fetchedCount.Load()), stageFetchedAttr)
	pagesCounter.Add(ctx, int64(fetchSkippedCount.Load()), stageFetchSkippedAttr)
	pagesCounter.Add(ctx, int64(copiedCount.Load()), stageCopiedAttr)
	pagesCounter.Add(ctx, int64(copySkippedCount.Load()), stageCopySkippedAttr)
	durationHistogram.Record(ctx, fetchDuration.Milliseconds(), phaseFetchAttr)
	if cs := copyStart.Load(); cs != nil {
		durationHistogram.Record(ctx, time.Since(*cs).Milliseconds(), phaseCopyAttr)
	}
	durationHistogram.Record(ctx, time.Since(runStart).Milliseconds(), phaseTotalAttr)

	p.logger.Debug(ctx, "prefetch: completed",
		zap.Uint64("fetched", fetchedCount.Load()),
		zap.Uint64("copied", copiedCount.Load()),
		zap.Uint64("fetch_skipped", fetchSkippedCount.Load()),
		zap.Uint64("copy_skipped", copySkippedCount.Load()),
		zap.Int("total", totalPages),
	)

	return nil
}

// startCopyWorkers waits for uffd to be ready, then starts copy workers
func (p *Prefetcher) startCopyWorkers(
	ctx context.Context,
	cancelRun context.CancelFunc,
	copyCh chan prefetchData,
	maxCopyWorkers int,
	copyStart *atomic.Pointer[time.Time],
	copiedCount *atomic.Uint64,
	copySkippedCount *atomic.Uint64,
) {
	// Wait for uffd to be ready
	select {
	case <-ctx.Done():
		return
	case <-p.uffd.Ready():
	}

	now := time.Now()
	copyStart.Store(&now)

	p.logger.Debug(ctx, "prefetch: uffd ready, starting copy workers")

	// Start copy workers
	var copyWorkerWg sync.WaitGroup

	for range maxCopyWorkers {
		copyWorkerWg.Go(func() {
			p.copyWorker(ctx, cancelRun, copyCh, copiedCount, copySkippedCount)
		})
	}

	copyWorkerWg.Wait()
}

// fetchWorker fetches extents from the source to populate the cache. An
// extent is one contiguous run of blocks fetched in a single source.Slice
// call; with coalescing off (the default) every extent is a single block,
// reproducing the plain per-block fetch. The copy phase always stays
// per-block: an extent is split into page-sized sub-slices, since UFFDIO_COPY
// requires page-sized data.
func (p *Prefetcher) fetchWorker(
	ctx context.Context,
	fetchCh <-chan extent,
	copyCh chan<- prefetchData,
	blockSize int64,
	fetchedCount *atomic.Uint64,
	skippedCount *atomic.Uint64,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-fetchCh:
			if !ok {
				return
			}

			baseOffset := header.BlockOffset(int64(e.startIdx), blockSize)

			// Fetch from source - this populates the cache. A multi-block
			// extent is one larger sequential read spanning e.blocks pages.
			data, err := p.source.Slice(ctx, baseOffset, blockSize*int64(e.blocks))
			if err != nil {
				p.logger.Debug(ctx, "prefetch: failed to fetch extent",
					zap.Int64("offset", baseOffset),
					zap.Int("blocks", e.blocks),
					zap.Error(err),
				)
				skippedCount.Add(uint64(e.blocks))

				continue
			}

			fetchedCount.Add(uint64(e.blocks))

			// Queue each page for copy (non-blocking - channel has enough
			// capacity).
			for b := range e.blocks {
				off := baseOffset + int64(b)*blockSize
				sub := data[int64(b)*blockSize : int64(b+1)*blockSize]
				select {
				case copyCh <- prefetchData{offset: off, data: sub}:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// copyWorker copies pages to guest memory via uffd
func (p *Prefetcher) copyWorker(
	ctx context.Context,
	cancelRun context.CancelFunc,
	copyCh <-chan prefetchData,
	copiedCount *atomic.Uint64,
	skippedCount *atomic.Uint64,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-copyCh:
			if !ok {
				return
			}

			installed, err := p.uffd.Prefault(ctx, d.offset, d.data)
			if errors.Is(err, userfaultfd.ErrClosed) {
				// The uffd is gone (sandbox teardown): every remaining queued
				// page would hit the same path. Cancel the run so the fetch
				// workers stop pulling pages nobody will copy, and count
				// nothing, keeping stage="copied" consistent with the
				// per-page prefault metric, which skips this path too.
				cancelRun()

				return
			}
			if err != nil {
				p.logger.Debug(ctx, "prefetch: failed to copy page",
					zap.Int64("offset", d.offset),
					zap.Error(err),
				)
				skippedCount.Add(1)

				continue
			}

			// A nil error doesn't mean this prefault copied the page: it may
			// have been skipped (already resident), lost the install race to
			// a demand fault, or deferred on EAGAIN. Count those as skipped so
			// stage="copied" matches prefault{result="installed"} exactly.
			if installed {
				copiedCount.Add(1)
			} else {
				skippedCount.Add(1)
			}
		}
	}
}

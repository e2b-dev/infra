package prefetch

import (
	"context"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/prefetch")

type prefetchData struct {
	offset int64
	data   []byte
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

	// Get worker counts from feature flags at runtime
	maxFetchWorkers := p.featureFlags.IntFlag(ctx, featureflags.MemoryPrefetchMaxFetchWorkers)
	maxCopyWorkers := p.featureFlags.IntFlag(ctx, featureflags.MemoryPrefetchMaxCopyWorkers)

	blockSize := p.mapping.BlockSize
	totalPages := len(indices)

	span.SetAttributes(
		attribute.Int64("prefetch.total_pages", int64(totalPages)),
		attribute.Int64("prefetch.block_size", blockSize),
		attribute.Int("prefetch.max_fetch_workers", maxFetchWorkers),
		attribute.Int("prefetch.max_copy_workers", maxCopyWorkers),
	)

	p.logger.Debug(ctx, "prefetch: starting background prefetch",
		zap.Int("total_pages", totalPages),
		zap.Int64("block_size", blockSize),
		zap.Int("max_fetch_workers", maxFetchWorkers),
		zap.Int("max_copy_workers", maxCopyWorkers),
	)

	// Channels for work distribution
	// Fetch channel: all offsets to fetch (large buffer so main goroutine doesn't block)
	fetchCh := make(chan int64, totalPages)
	// Copy channel: offsets ready to copy (fetched successfully)
	copyCh := make(chan prefetchData, totalPages)

	// Counters for statistics
	var fetchedCount atomic.Uint64
	var copiedCount atomic.Uint64
	var fetchSkippedCount atomic.Uint64
	var copySkippedCount atomic.Uint64

	var fetchWg sync.WaitGroup
	var copyWg sync.WaitGroup

	// Queue all offsets to fetch in the order they were originally accessed
	for _, idx := range indices {
		fetchCh <- header.BlockOffset(int64(idx), blockSize)
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
		p.startCopyWorkers(ctx, copyCh, maxCopyWorkers, &copiedCount, &copySkippedCount)
	})

	// Wait for fetch workers to complete
	fetchWg.Wait()
	// Close copy channel when all fetch workers are done
	close(copyCh)

	// Wait for copy workers to complete
	copyWg.Wait()

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
	copyCh chan prefetchData,
	maxCopyWorkers int,
	copiedCount *atomic.Uint64,
	copySkippedCount *atomic.Uint64,
) {
	// Wait for uffd to be ready
	select {
	case <-ctx.Done():
		return
	case <-p.uffd.Ready():
	}

	p.logger.Debug(ctx, "prefetch: uffd ready, starting copy workers")

	// Start copy workers
	var copyWorkerWg sync.WaitGroup

	for range maxCopyWorkers {
		copyWorkerWg.Go(func() {
			p.copyWorker(ctx, copyCh, copiedCount, copySkippedCount)
		})
	}

	copyWorkerWg.Wait()
}

// fetchWorker fetches pages from the source to populate the cache
func (p *Prefetcher) fetchWorker(
	ctx context.Context,
	fetchCh <-chan int64,
	copyCh chan<- prefetchData,
	blockSize int64,
	fetchedCount *atomic.Uint64,
	skippedCount *atomic.Uint64,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case offset, ok := <-fetchCh:
			if !ok {
				return
			}

			// Fetch from source - this populates the cache
			data, err := p.source.Slice(ctx, offset, blockSize)
			if err != nil {
				p.logger.Debug(ctx, "prefetch: failed to fetch page",
					zap.Int64("offset", offset),
					zap.Error(err),
				)
				skippedCount.Add(1)

				continue
			}

			fetchedCount.Add(1)

			// Queue for copy (non-blocking - channel has enough capacity)
			select {
			case copyCh <- prefetchData{offset: offset, data: data}:
			case <-ctx.Done():
			}
		}
	}
}

// copyWorker copies pages to guest memory via uffd
func (p *Prefetcher) copyWorker(
	ctx context.Context,
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

			err := p.uffd.Prefault(ctx, d.offset, d.data)
			if err != nil {
				p.logger.Debug(ctx, "prefetch: failed to copy page",
					zap.Int64("offset", d.offset),
					zap.Error(err),
				)
				skippedCount.Add(1)

				continue
			}

			copiedCount.Add(1)
		}
	}
}

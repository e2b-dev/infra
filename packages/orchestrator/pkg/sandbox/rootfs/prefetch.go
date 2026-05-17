//go:build linux

package rootfs

import (
	"context"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// Prefetcher walks a workload-derived prefetch mapping for the rootfs source
// and issues Slice calls so the chunker cache is warm before the guest
// demands the same blocks via NBD. Unlike the memory prefetcher there is no
// copy / prefault phase — populating the source's cache is enough; NBD
// reads on those blocks hit the warm cache directly.
type Prefetcher struct {
	logger       logger.Logger
	source       block.Slicer
	mapping      *metadata.MemoryPrefetchMapping
	featureFlags *featureflags.Client
}

func NewPrefetcher(
	log logger.Logger,
	source block.Slicer,
	mapping *metadata.MemoryPrefetchMapping,
	flags *featureflags.Client,
) *Prefetcher {
	return &Prefetcher{logger: log, source: source, mapping: mapping, featureFlags: flags}
}

// Start runs the prefetcher to completion or until ctx is cancelled.
// Fire-and-forget — errors are logged at debug.
func (p *Prefetcher) Start(ctx context.Context) {
	if p.mapping == nil {
		return
	}
	indices := p.mapping.Indices
	if len(indices) == 0 {
		return
	}

	ctx = storage.WithSkipCacheWriteback(ctx)
	ctx, span := tracer.Start(ctx, "rootfs-prefetch")
	defer span.End()

	blockSize := p.mapping.BlockSize
	if blockSize <= 0 {
		return
	}

	// Apply the same byte cap the memory prefetcher uses so a runaway
	// mapping cannot blow the per-resume bandwidth budget.
	if maxBytes := int64(p.featureFlags.IntFlag(ctx, featureflags.MemoryPrefetchMaxBytes)); maxBytes > 0 {
		maxBlocks := maxBytes / blockSize
		if maxBlocks > 0 && int64(len(indices)) > maxBlocks {
			indices = indices[:maxBlocks]
		}
	}

	maxWorkers := p.featureFlags.IntFlag(ctx, featureflags.RootfsPrefetchMaxWorkers)
	if maxWorkers <= 0 {
		return
	}

	span.SetAttributes(
		attribute.Int("rootfs.prefetch.total_blocks", len(indices)),
		attribute.Int("rootfs.prefetch.workers", maxWorkers),
		attribute.Int64("rootfs.prefetch.block_size", blockSize),
	)

	jobs := make(chan int64, maxWorkers)
	var fetched, failed atomic.Int64

	var wg sync.WaitGroup
	for range maxWorkers {
		wg.Go(func() {
			for off := range jobs {
				if _, err := p.source.Slice(ctx, off, blockSize); err != nil {
					failed.Add(1)

					continue
				}
				fetched.Add(1)
			}
		})
	}

	for _, idx := range indices {
		select {
		case <-ctx.Done():
		case jobs <- header.BlockOffset(int64(idx), blockSize):
		}
	}
	close(jobs)
	wg.Wait()

	p.logger.Debug(ctx, "rootfs prefetch completed",
		zap.Int64("fetched", fetched.Load()),
		zap.Int64("failed", failed.Load()),
		zap.Int("total", len(indices)),
	)
}

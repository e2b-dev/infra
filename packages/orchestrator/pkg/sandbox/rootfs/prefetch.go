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

// Prefetcher warms the rootfs chunker cache by issuing Slice calls against
// the prefetch mapping. No copy phase: populating the chunker cache is
// enough; subsequent NBD reads hit it.
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

func (p *Prefetcher) Start(ctx context.Context) {
	if p.mapping == nil || len(p.mapping.Indices) == 0 || p.mapping.BlockSize <= 0 {
		return
	}

	ctx = storage.WithSkipCacheWriteback(ctx)
	ctx, span := tracer.Start(ctx, "rootfs-prefetch")
	defer span.End()

	indices := p.mapping.Indices
	blockSize := p.mapping.BlockSize

	if maxBytes := int64(p.featureFlags.IntFlag(ctx, featureflags.MemoryPrefetchMaxBytes)); maxBytes > 0 {
		if maxBlocks := maxBytes / blockSize; maxBlocks > 0 && int64(len(indices)) > maxBlocks {
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

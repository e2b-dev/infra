package sandbox

import (
	"bytes"
	"context"
	"fmt"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// writeDedupDiff compares exported dirty memory blocks against the original template
// at 4KiB page granularity and writes only pages that actually differ.
//
// The exported cache contains dirty blocks (at blockSize granularity, e.g. 2MB hugepages)
// sequentially. For each dirty block, we compare every 4KiB page against the original
// template and only include pages that changed. This can dramatically reduce diff sizes
// when pages within a dirty hugepage haven't actually been modified.
func writeDedupDiff(
	ctx context.Context,
	exportedCache *block.Cache,
	originalMemfile storage.SeekableReader,
	dirtyBlocks *bitset.BitSet,
	blockSize int64,
	totalMemorySize int64,
	outPath string,
) (*block.Cache, *header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "write-dedup-diff")
	defer span.End()

	totalPageCount := uint(header.TotalBlocks(totalMemorySize, header.PageSize))
	pageDirty := bitset.New(totalPageCount)

	srcBuf := make([]byte, blockSize)
	origBuf := make([]byte, blockSize)

	// First pass: count unique pages to size the output cache
	var cacheOffset int64
	var totalPages, dedupedPages uint
	var uniquePageCount int64

	for i, ok := dirtyBlocks.NextSet(0); ok; i, ok = dirtyBlocks.NextSet(i + 1) {
		memOffset := int64(i) * blockSize

		_, err := exportedCache.ReadAt(srcBuf, cacheOffset)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read from exported cache at offset %d: %w", cacheOffset, err)
		}

		_, err = originalMemfile.ReadAt(ctx, origBuf, memOffset)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read from original memfile at offset %d: %w", memOffset, err)
		}

		for j := int64(0); j < blockSize; j += header.PageSize {
			totalPages++
			if bytes.Equal(srcBuf[j:j+header.PageSize], origBuf[j:j+header.PageSize]) {
				dedupedPages++

				continue
			}
			pageIdx := uint(memOffset+j) / uint(header.PageSize)
			pageDirty.Set(pageIdx)
			uniquePageCount++
		}

		cacheOffset += blockSize
	}

	// Create output cache with exact size
	dedupSize := uniquePageCount * header.PageSize
	dedupCache, err := block.NewCache(dedupSize, header.PageSize, outPath, false)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create dedup cache: %w", err)
	}

	// Second pass: write unique pages to cache
	cacheOffset = 0
	var writeOffset int64

	for i, ok := dirtyBlocks.NextSet(0); ok; i, ok = dirtyBlocks.NextSet(i + 1) {
		memOffset := int64(i) * blockSize

		exportedCache.ReadAt(srcBuf, cacheOffset)
		originalMemfile.ReadAt(ctx, origBuf, memOffset)

		for j := int64(0); j < blockSize; j += header.PageSize {
			if bytes.Equal(srcBuf[j:j+header.PageSize], origBuf[j:j+header.PageSize]) {
				continue
			}
			dedupCache.WriteAt(srcBuf[j:j+header.PageSize], writeOffset)
			writeOffset += header.PageSize
		}

		cacheOffset += blockSize
	}

	exportedSize := int64(dirtyBlocks.Count()) * blockSize

	telemetry.SetAttributes(ctx,
		attribute.Int64("dedup.total_pages", int64(totalPages)),
		attribute.Int64("dedup.deduped_pages", int64(dedupedPages)),
		attribute.Int64("dedup.unique_pages", uniquePageCount),
		attribute.Float64("dedup.ratio", safeDivide(float64(dedupedPages), float64(totalPages))),
	)

	logger.L().Info(ctx, "4KiB page dedup completed",
		zap.Uint("dirty_blocks", dirtyBlocks.Count()),
		zap.Uint("total_4k_pages", totalPages),
		zap.Uint("deduped_pages", dedupedPages),
		zap.Int64("unique_pages", uniquePageCount),
		zap.Int64("exported_size_bytes", exportedSize),
		zap.Int64("dedup_size_bytes", dedupSize),
		zap.String("reduction", fmt.Sprintf("%.1f%%", safeDivide(float64(dedupedPages), float64(totalPages))*100)),
	)

	// Every page NOT in pageDirty must be mapped to uuid.Nil (zeros)
	pageEmpty := bitset.New(totalPageCount)
	pageEmpty.FlipRange(0, totalPageCount)
	pageEmpty.InPlaceDifference(pageDirty)

	return dedupCache, &header.DiffMetadata{
		Dirty:     pageDirty,
		Empty:     pageEmpty,
		BlockSize: header.PageSize,
	}, nil
}

func safeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}

	return a / b
}

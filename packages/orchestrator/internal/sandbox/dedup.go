package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
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
	outPath string,
) (*header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "write-dedup-diff")
	defer span.End()

	outFile, err := os.Create(outPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create dedup diff file: %w", err)
	}
	defer outFile.Close()

	pageDirty := bitset.New(0)

	srcBuf := make([]byte, blockSize)
	origBuf := make([]byte, blockSize)

	var cacheOffset int64
	var totalPages, dedupedPages uint

	for i, ok := dirtyBlocks.NextSet(0); ok; i, ok = dirtyBlocks.NextSet(i + 1) {
		memOffset := int64(i) * blockSize

		_, err := exportedCache.ReadAt(srcBuf, cacheOffset)
		if err != nil {
			return nil, fmt.Errorf("failed to read from exported cache at offset %d: %w", cacheOffset, err)
		}

		_, err = originalMemfile.ReadAt(ctx, origBuf, memOffset)
		if err != nil {
			return nil, fmt.Errorf("failed to read from original memfile at offset %d: %w", memOffset, err)
		}

		for j := int64(0); j < blockSize; j += header.PageSize {
			totalPages++

			if bytes.Equal(srcBuf[j:j+header.PageSize], origBuf[j:j+header.PageSize]) {
				dedupedPages++
				continue
			}

			pageIdx := uint(memOffset+j) / uint(header.PageSize)
			pageDirty.Set(pageIdx)

			_, err = outFile.Write(srcBuf[j : j+header.PageSize])
			if err != nil {
				return nil, fmt.Errorf("failed to write dedup page: %w", err)
			}
		}

		cacheOffset += blockSize
	}

	uniquePages := totalPages - dedupedPages
	exportedSize := int64(dirtyBlocks.Count()) * blockSize
	dedupSize := int64(uniquePages) * header.PageSize

	telemetry.SetAttributes(ctx,
		attribute.Int64("dedup.total_pages", int64(totalPages)),
		attribute.Int64("dedup.deduped_pages", int64(dedupedPages)),
		attribute.Int64("dedup.unique_pages", int64(uniquePages)),
		attribute.Float64("dedup.ratio", safeDivide(float64(dedupedPages), float64(totalPages))),
	)

	logger.L().Info(ctx, "4KiB page dedup completed",
		zap.Uint("dirty_blocks", dirtyBlocks.Count()),
		zap.Uint("total_4k_pages", totalPages),
		zap.Uint("deduped_pages", dedupedPages),
		zap.Uint("unique_pages", uniquePages),
		zap.Int64("exported_size_bytes", exportedSize),
		zap.Int64("dedup_size_bytes", dedupSize),
		zap.String("reduction", fmt.Sprintf("%.1f%%", safeDivide(float64(dedupedPages), float64(totalPages))*100)),
	)

	return &header.DiffMetadata{
		Dirty:     pageDirty,
		Empty:     bitset.New(0),
		BlockSize: header.PageSize,
	}, nil
}

// newDedupDiffFromFile creates a Diff from a dedup diff file on disk.
func newDedupDiffFromFile(cacheKey build.DiffStoreKey, filePath string) (build.Diff, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat dedup diff file: %w", err)
	}

	cache, err := block.NewCache(info.Size(), header.PageSize, filePath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache from dedup diff: %w", err)
	}

	return build.NewLocalDiffFromCache(cacheKey, cache)
}

func safeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}


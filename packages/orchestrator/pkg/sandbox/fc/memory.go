//go:build linux

package fc

import (
	"context"
	"errors"
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// MemoryInfo returns the memory info for the sandbox.
// The dirty field represents mincore resident pages—essentially pages that were faulted in.
// The empty field represents pages that are *resident*, but also completely empty.
func (p *Process) MemoryInfo(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	return p.client.memoryInfo(ctx, blockSize)
}

func (p *Process) DirtyMemory(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	return p.client.dirtyMemory(ctx, blockSize)
}

func (p *Process) exportMemoryFromFc(
	ctx context.Context,
	include *roaring.Bitmap,
	cachePath string,
	blockSize int64,
) (*block.Cache, error) {
	ctx, span := tracer.Start(ctx, "export-memory-from-fc")
	defer span.End()

	m, err := p.client.memoryMapping(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory mappings: %w", err)
	}

	var remoteRanges []block.Range

	for r := range block.BitsetRanges(include, blockSize) {
		hostVirtRanges, err := m.GetHostVirtRanges(r.Start, r.Size)
		if err != nil {
			return nil, fmt.Errorf("failed to get host virt ranges: %w", err)
		}

		remoteRanges = append(remoteRanges, hostVirtRanges...)
	}

	pid, err := p.Pid()
	if err != nil {
		return nil, fmt.Errorf("failed to get pid: %w", err)
	}

	cache, err := block.NewCacheFromProcessMemory(ctx, blockSize, cachePath, pid, remoteRanges)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return cache, nil
}

// ExportMemory writes dirty guest memory to cachePath. If originalMemfile
// is non-nil the result is deduplicated and DiffMetadata is non-nil;
// mutually exclusive with bgCopy. When dedupBestEffort is true, blocks whose
// base data isn't already in the chunker's local cache are written through
// as-is (see block.dedupPages).
func (p *Process) ExportMemory(
	ctx context.Context,
	include *roaring.Bitmap,
	cachePath string,
	blockSize int64,
	memfd *block.Memfd,
	bgCopy bool,
	originalMemfile block.ReadonlyDevice,
	dedupBestEffort bool,
	dedupDirectIO bool,
) (block.DiffSource, *header.DiffMetadata, error) {
	if memfd != nil {
		if originalMemfile != nil {
			return block.NewCacheFromMemfdDeduped(ctx, originalMemfile, blockSize, cachePath, memfd, include, dedupBestEffort, dedupDirectIO)
		}
		if bgCopy {
			src, err := block.NewCacheFromMemfdAsync(ctx, blockSize, cachePath, memfd, include)

			return src, nil, err
		}
		src, err := block.NewCacheFromMemfd(ctx, blockSize, cachePath, memfd, include)

		return src, nil, err
	}

	cache, err := p.exportMemoryFromFc(ctx, include, cachePath, blockSize)
	if err != nil {
		return nil, nil, err
	}
	if originalMemfile == nil {
		return cache, nil, nil
	}
	// .dedup suffix avoids clobbering the source mmap during truncate.
	dedupCache, meta, err := cache.Dedup(ctx, originalMemfile, include, blockSize, cachePath+".dedup", dedupBestEffort, dedupDirectIO)
	if err != nil {
		return nil, nil, fmt.Errorf("dedup memfile diff: %w", errors.Join(err, cache.Close()))
	}
	if err := cache.Close(); err != nil {
		return nil, nil, fmt.Errorf("close pre-dedup cache: %w", errors.Join(err, dedupCache.Close()))
	}

	return dedupCache, meta, nil
}

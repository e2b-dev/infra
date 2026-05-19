//go:build linux

package fc

import (
	"context"
	"errors"
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
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

func (p *Process) exportMemoryFromMemfd(
	ctx context.Context,
	include *roaring.Bitmap,
	cachePath string,
	blockSize int64,
	memfd *block.Memfd,
	originalMemfile block.ReadonlyDevice,
) (*block.Cache, *header.DiffMetadata, error) {
	if originalMemfile == nil {
		cache, err := block.NewCacheFromMemfd(ctx, blockSize, cachePath, memfd, include)
		if err != nil {
			return nil, nil, err
		}

		return cache, nil, nil
	}

	cache, diffMeta, err := block.NewCacheFromMemfdDeduped(ctx, originalMemfile, blockSize, cachePath, memfd, include)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create deduped MemfdCache: %w", err)
	}

	return cache, diffMeta, nil
}

func (p *Process) exportMemoryFromFc(
	ctx context.Context,
	include *roaring.Bitmap,
	cachePath string,
	blockSize int64,
) (*block.Cache, error) {
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

// ExportMemory writes dirty guest memory to a local cache file and, if
// originalMemfile != nil, deduplicates the result against it; in that case
// the returned DiffMetadata is non-nil and page-granular.
func (p *Process) ExportMemory(
	ctx context.Context,
	buildID uuid.UUID,
	include *roaring.Bitmap,
	cacheDir string,
	blockSize int64,
	memfd *block.Memfd,
	originalMemfile block.ReadonlyDevice,
) (*block.Cache, *header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "export-memory")
	defer span.End()

	cachePath := build.GenerateDiffCachePath(cacheDir, buildID.String(), build.Memfile)

	if memfd != nil {
		return p.exportMemoryFromMemfd(ctx, include, cachePath, blockSize, memfd, originalMemfile)
	}

	cache, err := p.exportMemoryFromFc(ctx, include, cachePath, blockSize)
	if err != nil {
		return nil, nil, err
	}

	if originalMemfile == nil {
		return cache, nil, nil
	}

	// Fresh suffix so Dedup's NewCache truncate doesn't clobber the source mmap.
	dedupPath := build.GenerateDiffCachePath(cacheDir, buildID.String(), build.Memfile)
	dedupCache, dedupMetadata, err := cache.Dedup(ctx, originalMemfile, include, blockSize, dedupPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dedup memfile diff: %w", errors.Join(err, cache.Close()))
	}
	if err := cache.Close(); err != nil {
		return nil, nil, fmt.Errorf("failed to close pre-dedup cache: %w", errors.Join(err, dedupCache.Close()))
	}

	return dedupCache, dedupMetadata, nil
}

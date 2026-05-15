//go:build linux

package fc

import (
	"context"
	"errors"
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

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

// exportMemoryFromMemfd dispatches between the plain and the deduped memfd
// cache constructors. Both take ownership of `memfd` (close it internally),
// so this wrapper does not.
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

// ExportMemory writes the dirty guest-memory regions to a local cache file
// and optionally deduplicates the result against `originalMemfile`.
//
// When dedup runs, the returned *header.DiffMetadata is non-nil and reflects
// the page-granular result; otherwise it is nil and the caller keeps the
// pre-export diff metadata.
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

	dedup := originalMemfile != nil
	span.SetAttributes(
		attribute.Bool("export.memfd", memfd != nil),
		attribute.Bool("export.dedup", dedup),
	)

	cachePath := build.GenerateDiffCachePath(cacheDir, buildID.String(), build.Memfile)

	if memfd != nil {
		return p.exportMemoryFromMemfd(ctx, include, cachePath, blockSize, memfd, originalMemfile)
	}

	cache, err := p.exportMemoryFromFc(ctx, include, cachePath, blockSize)
	if err != nil {
		return nil, nil, err
	}

	if !dedup {
		return cache, nil, nil
	}

	// Use a fresh path for the dedup output. GenerateDiffCachePath appends
	// a random suffix per call, so dedupPath != cachePath — the truncate
	// inside NewCache (called from Dedup) won't clobber the source file
	// while its mmap is still being read.
	dedupPath := build.GenerateDiffCachePath(cacheDir, buildID.String(), build.Memfile)
	dedupCache, dedupMetadata, err := cache.Dedup(ctx, originalMemfile, include, blockSize, dedupPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dedup memfile diff: %w", errors.Join(err, cache.Close()))
	}

	// Cache.Close removes the underlying file, so no separate os.Remove needed.
	if err := cache.Close(); err != nil {
		return nil, nil, fmt.Errorf("failed to close pre-dedup cache: %w", errors.Join(err, dedupCache.Close()))
	}

	return dedupCache, dedupMetadata, nil
}

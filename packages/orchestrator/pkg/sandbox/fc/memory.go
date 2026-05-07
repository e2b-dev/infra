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

func (p *Process) exportMemoryFromMemfd(
	ctx context.Context,
	include *roaring.Bitmap,
	cachePath string,
	blockSize int64,
	memfd *block.Memfd,
) (*block.Cache, error) {
	var guestRanges []block.Range
	for r := range block.BitsetRanges(include, blockSize) {
		guestRanges = append(guestRanges, r)
	}

	cache, err := block.NewCacheFromMemfd(ctx, blockSize, cachePath, memfd, guestRanges)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", errors.Join(err, memfd.Close()))
	}

	err = memfd.Close()
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}

	return cache, nil
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

func (p *Process) ExportMemory(
	ctx context.Context,
	include *roaring.Bitmap,
	cachePath string,
	blockSize int64,
	memfd *block.Memfd,
) (*block.Cache, error) {
	if memfd == nil {
		return p.exportMemoryFromFc(ctx, include, cachePath, blockSize)
	}

	return p.exportMemoryFromMemfd(ctx, include, cachePath, blockSize, memfd)
}

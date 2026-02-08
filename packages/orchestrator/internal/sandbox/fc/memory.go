package fc

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// InitialMemoryMetadata returns the memory info for the sandbox.
// The dirty field represents mincore resident pages—essentially pages that were faulted in.
// The empty field represents pages that are *resident*, but also completely empty.
func (p *Process) InitialMemoryMetadata(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	return p.client.initialMemory(ctx, blockSize)
}

func (p *Process) ExportMemory(ctx context.Context, cachePath string) (*block.Cache, *header.DiffMetadata, error) {
	m, err := p.client.memoryMapping(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get memory mappings: %w", err)
	}

	pageSize, err := m.PageSize()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get block size: %w", err)
	}

	diff, err := p.client.dirtyMemory(ctx, pageSize)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get dirty memory metadata: %w", err)
	}

	var remoteRanges []block.Range

	for r := range block.BitsetRanges(diff.Dirty, pageSize) {
		hostVirtRanges, err := m.GetHostVirtRanges(r.Start, r.Size)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get host virt ranges: %w", err)
		}

		remoteRanges = append(remoteRanges, hostVirtRanges...)
	}

	pid, err := p.Pid()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get pid: %w", err)
	}

	cache, err := block.NewCacheFromProcessMemory(ctx, pageSize, cachePath, pid, remoteRanges)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return cache, diff, nil
}

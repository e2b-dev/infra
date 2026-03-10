package uffd

import (
	"context"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type NoopMemory struct {
	size      int64
	blockSize int64

	exit *utils.ErrorOnce
}

var _ MemoryBackend = (*NoopMemory)(nil)

func NewNoopMemory(size, blockSize int64) *NoopMemory {
	return &NoopMemory{
		size:      size,
		blockSize: blockSize,
		exit:      utils.NewErrorOnce(),
	}
}

func (m *NoopMemory) Prefault(_ context.Context, _ int64, _ []byte) error {
	return nil
}

func (m *NoopMemory) DiffMetadata(ctx context.Context, f *fc.Process) (*header.DiffMetadata, error) {
	diffInfo, err := f.MemoryInfo(ctx, m.blockSize)
	if err != nil {
		return nil, err
	}

	numberOfPages := header.TotalBlocks(m.size, m.blockSize)

	// diffInfo.Dirty = Resident pages (via mincore)
	// diffInfo.Empty = Resident AND all-zero pages (subset of Resident)
	//
	// For a fresh-boot VM, non-resident pages may still contain data written by
	// Firecracker (kernel image, virtio ring buffers, boot parameters) or by the
	// guest kernel before those pages were evicted from host memory. These pages
	// MUST be exported via process_vm_readv (which transparently handles swapped-out
	// pages) rather than being assumed empty.
	//
	// dirty  = all pages that are NOT confirmed empty (resident non-zero + non-resident)
	// empty  = only pages confirmed to be resident AND all-zeros

	empty := diffInfo.Empty.Clone()

	allPages := bitset.New(uint(numberOfPages))
	allPages.FlipRange(0, uint(numberOfPages))

	dirty := allPages.Difference(empty)

	return &header.DiffMetadata{
		Dirty:     dirty,
		Empty:     empty,
		BlockSize: m.blockSize,
	}, nil
}

func (m *NoopMemory) PrefetchData(_ context.Context) (block.PrefetchData, error) {
	// NoopMemory doesn't track block accesses, so return empty data
	return block.PrefetchData{
		BlockEntries: nil,
		BlockSize:    m.blockSize,
	}, nil
}

func (m *NoopMemory) Start(context.Context, string) error {
	return nil
}

func (m *NoopMemory) Stop() error {
	m.exit.SetSuccess()

	// This should be idempotent, so no need to return an error if it's already set.
	return nil
}

func (m *NoopMemory) Ready() chan struct{} {
	ch := make(chan struct{})
	close(ch)

	return ch
}

func (m *NoopMemory) Exit() *utils.ErrorOnce {
	return m.exit
}

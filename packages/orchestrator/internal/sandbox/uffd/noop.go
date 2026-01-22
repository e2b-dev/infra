package uffd

import (
	"context"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type NoopMemory struct {
	size      int64
	blockSize int64

	exit            *utils.ErrorOnce
	getDiffMetadata func(ctx context.Context, blockSize int64) (*header.DiffMetadata, error)
}

var _ MemoryBackend = (*NoopMemory)(nil)

func NewNoopMemory(
	size,
	blockSize int64,
	getDiffMetadata func(ctx context.Context, blockSize int64) (*header.DiffMetadata, error),
) *NoopMemory {
	return &NoopMemory{
		size:            size,
		blockSize:       blockSize,
		exit:            utils.NewErrorOnce(),
		getDiffMetadata: getDiffMetadata,
	}
}

func (m *NoopMemory) Prefault(_ context.Context, _ int64, _ []byte) error {
	return nil
}

func (m *NoopMemory) DiffMetadata(ctx context.Context) (*header.DiffMetadata, error) {
	diffInfo, err := m.getDiffMetadata(ctx, m.blockSize)
	if err != nil {
		return nil, err
	}

	dirty := diffInfo.Dirty.Difference(diffInfo.Empty)

	numberOfPages := header.TotalBlocks(m.size, m.blockSize)

	empty := bitset.New(uint(numberOfPages))
	empty.FlipRange(0, uint(numberOfPages))

	empty = empty.Difference(dirty)

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

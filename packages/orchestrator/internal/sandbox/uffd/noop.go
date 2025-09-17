package uffd

import (
	"context"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type NoopMemory struct {
	size      int64
	blockSize int64

	dirty *bitset.BitSet

	exit *utils.ErrorOnce
}

var _ MemoryBackend = (*NoopMemory)(nil)

func NewNoopMemory(size, blockSize int64) *NoopMemory {
	blocks := header.TotalBlocks(size, blockSize)

	dirty := bitset.New(uint(blocks))
	dirty.FlipRange(0, dirty.Len())

	return &NoopMemory{
		size:      size,
		blockSize: blockSize,
		dirty:     dirty,
		exit:      utils.NewErrorOnce(),
	}
}

func (m *NoopMemory) Disable() error {
	return nil
}

func (m *NoopMemory) Dirty() *bitset.BitSet {
	return m.dirty
}

func (m *NoopMemory) Start(ctx context.Context, sandboxId string) error {
	return nil
}

func (m *NoopMemory) Stop() error {
	return m.exit.SetSuccess()
}

func (m *NoopMemory) Ready() chan struct{} {
	ch := make(chan struct{})
	ch <- struct{}{}
	return ch
}

func (m *NoopMemory) Exit() *utils.ErrorOnce {
	return m.exit
}

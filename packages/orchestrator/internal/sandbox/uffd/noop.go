package uffd

import (
	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type NoopMemory struct {
	size      int64
	blockSize int64

	dirty *bitset.BitSet
}

func NewNoopMemory(size, blockSize int64) *NoopMemory {
	blocks := header.TotalBlocks(size, blockSize)

	dirty := bitset.New(0)
	for i := int64(0); i < blocks; i++ {
		dirty.Set(uint(i))
	}

	return &NoopMemory{
		size:      size,
		blockSize: blockSize,
		dirty:     dirty,
	}
}

func (m *NoopMemory) Disable() error {
	return nil
}

func (m *NoopMemory) Dirty() *bitset.BitSet {
	return m.dirty
}

func (m *NoopMemory) Start(sandboxId string) error {
	return nil
}

func (m *NoopMemory) Stop() error {
	return nil
}

func (m *NoopMemory) Ready() chan struct{} {
	ch := make(chan struct{})
	ch <- struct{}{}
	return ch
}

func (m *NoopMemory) Exit() chan error {
	return make(chan error)
}

package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type NoopMemory struct {
	size      int64
	blockSize int64

	dirty *block.Tracker

	exit *utils.ErrorOnce
}

var _ MemoryBackend = (*NoopMemory)(nil)

func NewNoopMemory(size, blockSize int64) *NoopMemory {
	return &NoopMemory{
		size:      size,
		blockSize: blockSize,
		dirty:     block.NewTracker(blockSize),
		exit:      utils.NewErrorOnce(),
	}
}

func (m *NoopMemory) Disable(context.Context) error {
	return nil
}

func (m *NoopMemory) Dirty(context.Context) (*block.Tracker, error) {
	return m.dirty.Clone(), nil
}

func (m *NoopMemory) Start(context.Context, string) error {
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

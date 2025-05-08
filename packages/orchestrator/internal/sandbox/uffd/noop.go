package uffd

import "github.com/bits-and-blooms/bitset"

type NoopMemory struct {
}

func (m *NoopMemory) Disable() error {
	return nil
}

func (m *NoopMemory) Dirty() *bitset.BitSet {
	return nil
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

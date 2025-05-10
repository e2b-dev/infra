package uffd

import "github.com/bits-and-blooms/bitset"

type MemoryBackend interface {
	Disable() error
	Dirty() *bitset.BitSet

	Start(sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() chan error
}

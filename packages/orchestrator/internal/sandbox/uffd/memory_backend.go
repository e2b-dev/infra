package uffd

import (
	"context"

	"github.com/bits-and-blooms/bitset"
)

type MemoryBackend interface {
	Disable() error
	Dirty() *bitset.BitSet

	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() chan error
}

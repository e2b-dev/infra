package uffd

import (
	"context"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	Disable() error
	Dirty() *bitset.BitSet

	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
}

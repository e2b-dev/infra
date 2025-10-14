package uffd

import (
	"context"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	// Dirty returns the dirty bitset.
	// It waits for all the requests in flight to be finished.
	Dirty(ctx context.Context) (*bitset.BitSet, error)
	// Disable switch the uffd to start serving empty pages and returns the dirty bitset.
	// It waits for all the requests in flight to be finished.
	Disable(ctx context.Context) (*bitset.BitSet, error)

	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
}

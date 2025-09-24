package block

import (
	"context"
	"sync"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TrackedSliceDevice struct {
	data      ReadonlyDevice
	blockSize int64

	dirty   *bitset.BitSet
	dirtyMu sync.Mutex
}

func NewTrackedSliceDevice(blockSize int64, device ReadonlyDevice) (*TrackedSliceDevice, error) {
	return &TrackedSliceDevice{
		data:      device,
		blockSize: blockSize,
		dirty:     bitset.New(0),
	}, nil
}

func (t *TrackedSliceDevice) Slice(ctx context.Context, off int64, length int64) ([]byte, error) {
	t.dirtyMu.Lock()
	t.dirty.Set(uint(header.BlockIdx(off, t.blockSize)))
	t.dirtyMu.Unlock()

	return t.data.Slice(ctx, off, length)
}

// Return which bytes were not read since Disable.
// This effectively returns the bytes that have been requested after paused vm and are not dirty.
func (t *TrackedSliceDevice) Dirty() *bitset.BitSet {
	t.dirtyMu.Lock()
	defer t.dirtyMu.Unlock()

	return t.dirty.Clone()
}

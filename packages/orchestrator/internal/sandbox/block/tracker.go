package block

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TrackedSliceDevice struct {
	data      ReadonlyDevice
	blockSize int64

	nilTracking atomic.Bool
	dirty       *bitset.BitSet
	dirtyMu     sync.Mutex
	empty       []byte
}

func NewTrackedSliceDevice(blockSize int64, device ReadonlyDevice) (*TrackedSliceDevice, error) {
	return &TrackedSliceDevice{
		data:      device,
		empty:     make([]byte, blockSize),
		blockSize: blockSize,
	}, nil
}

func (t *TrackedSliceDevice) Disable() error {
	size, err := t.data.Size()
	if err != nil {
		return fmt.Errorf("failed to get device size: %w", err)
	}

	t.dirty = bitset.New(uint(header.TotalBlocks(size, t.blockSize)))
	// We are starting with all being dirty.
	t.dirty.FlipRange(0, t.dirty.Len())

	t.nilTracking.Store(true)

	return nil
}

func (t *TrackedSliceDevice) Slice(ctx context.Context, off int64, length int64) ([]byte, error) {
	if t.nilTracking.Load() {
		t.dirtyMu.Lock()
		t.dirty.Clear(uint(header.BlockIdx(off, t.blockSize)))
		t.dirtyMu.Unlock()

		return t.empty, nil
	}

	return t.data.Slice(ctx, off, length)
}

// Return which bytes were not read since Disable.
// This effectively returns the bytes that have been requested after paused vm and are not dirty.
func (t *TrackedSliceDevice) Dirty() *bitset.BitSet {
	t.dirtyMu.Lock()
	defer t.dirtyMu.Unlock()

	return t.dirty.Clone()
}

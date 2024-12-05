package block

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/bits-and-blooms/bitset"
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
	size, err := device.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get device size: %w", err)
	}

	dirty := bitset.New(uint((size + blockSize - 1) / blockSize))
	// We are starting with all being dirty.
	dirty.FlipRange(0, uint(dirty.Len()))

	return &TrackedSliceDevice{
		data:      device,
		dirty:     dirty,
		empty:     make([]byte, blockSize),
		blockSize: blockSize,
	}, nil
}

func (t *TrackedSliceDevice) Disable() {
	t.nilTracking.Store(true)
}

func (t *TrackedSliceDevice) Slice(off int64, len int64) ([]byte, error) {
	if t.nilTracking.Load() {
		t.dirtyMu.Lock()
		t.dirty.Clear(uint(off / t.blockSize))
		t.dirtyMu.Unlock()

		return t.empty, nil
	}

	return t.data.Slice(off, len)
}

// Return which bytes were not read since Disable.
// This effectively returns the bytes that have been requested after paused vm and are not dirty.
func (t *TrackedSliceDevice) Dirty() *bitset.BitSet {
	return t.dirty
}

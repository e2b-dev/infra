package block

import (
	"errors"
	"fmt"
	"io"

	"github.com/bits-and-blooms/bitset"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/build/header"
)

type Overlay struct {
	device    ReadonlyDevice
	cache     *Cache
	blockSize int64
}

func NewOverlay(device ReadonlyDevice, cache *Cache, blockSize int64) *Overlay {
	return &Overlay{
		device:    device,
		cache:     cache,
		blockSize: blockSize,
	}
}

// TODO: Check the list block offsets during copying.
func (o *Overlay) ReadAt(p []byte, off int64) (int, error) {
	blocks := header.ListBlocks(off, int64(len(p)), o.blockSize)

	for i, blockOff := range blocks {
		n, err := o.cache.ReadAt(p[blockOff-off:blockOff+o.blockSize-off], blockOff)
		if err == nil {
			fmt.Printf("[overlay] (%d) > %d cache hit\n", i, blockOff)

			continue
		}

		if !errors.As(err, &ErrBytesNotAvailable{}) {
			return n, fmt.Errorf("error reading from cache: %w", err)
		}

		n, err = o.device.ReadAt(p[blockOff-off:blockOff+o.blockSize-off], blockOff)
		if err != nil {
			return n, fmt.Errorf("error reading from device: %w", err)
		}
	}

	return len(p), nil
}

func (o *Overlay) Export(out io.Writer) (*bitset.BitSet, error) {
	return o.cache.Export(out)
}

func (o *Overlay) Slice(off, length int64) ([]byte, error) {
	// This method will not be very optimal if the length is not the same as the block size, because we cannot be just exposing the cache slice,
	// but creating and copying the bytes from the cache and device to the new slice.

	// When we are implementing this we might want to just enforce the length to be the same as the block size.
	return nil, fmt.Errorf("not implemented")
}

func (o *Overlay) WriteAt(p []byte, off int64) (int, error) {
	return o.cache.WriteAt(p, off)
}

func (o *Overlay) Size() (int64, error) {
	return o.cache.Size()
}

func (o *Overlay) Close() error {
	return o.cache.Close()
}

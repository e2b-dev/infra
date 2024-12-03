package block

import (
	"errors"
	"fmt"
)

type Overlay struct {
	device    ReadonlyDevice
	cache     Device
	blockSize int64
}

func NewOverlay(device ReadonlyDevice, cache Device, blockSize int64) *Overlay {
	return &Overlay{
		device:    device,
		cache:     cache,
		blockSize: blockSize,
	}
}

func (o *Overlay) ReadAt(p []byte, off int64) (int, error) {
	blocks := listBlocks(off, off+int64(len(p)), o.blockSize)

	for i, block := range blocks {
		n, err := o.cache.ReadAt(p[block.start-off:block.end-off], block.start)
		if err == nil {
			fmt.Printf("[overlay] (%d) > %d cache hit\n", i, block.start)

			continue
		}

		if !errors.As(err, &ErrBytesNotAvailable{}) {
			return n, fmt.Errorf("error reading from cache: %w", err)
		}

		n, err = o.device.ReadAt(p[block.start-off:block.end-off], block.start)
		if err != nil {
			return n, fmt.Errorf("error reading from device: %w", err)
		}
	}

	return len(p), nil
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

func LayerDevices(blockSize int64, layers ...Device) (ReadonlyDevice, error) {
	if len(layers) == 0 {
		return nil, fmt.Errorf("at least one layer is required")
	}

	overlay := layers[0]

	for _, layer := range layers[1:] {
		overlay = NewOverlay(overlay, layer, blockSize)
	}

	return overlay, nil
}

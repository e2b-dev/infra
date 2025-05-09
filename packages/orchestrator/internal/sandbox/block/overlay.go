package block

import (
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Overlay struct {
	device       ReadonlyDevice
	cache        *Cache
	blockSize    int64
	cacheEjected atomic.Bool
}

func NewOverlay(device ReadonlyDevice, cache *Cache, blockSize int64) *Overlay {
	return &Overlay{
		device:    device,
		cache:     cache,
		blockSize: blockSize,
	}
}

func (o *Overlay) ReadAt(p []byte, off int64) (int, error) {
	blocks := header.BlocksOffsets(int64(len(p)), o.blockSize)

	for _, blockOff := range blocks {
		n, err := o.cache.ReadAt(p[blockOff:blockOff+o.blockSize], off+blockOff)
		if err == nil {
			continue
		}

		if !errors.As(err, &ErrBytesNotAvailable{}) {
			return n, fmt.Errorf("error reading from cache: %w", err)
		}

		n, err = o.device.ReadAt(p[blockOff:blockOff+o.blockSize], off+blockOff)
		if err != nil {
			return n, fmt.Errorf("error reading from device: %w", err)
		}
	}

	return len(p), nil
}

func (o *Overlay) EjectCache() (*Cache, error) {
	if !o.cacheEjected.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("cache already ejected")
	}

	return o.cache, nil
}

// This method will not be very optimal if the length is not the same as the block size, because we cannot be just exposing the cache slice,
// but creating and copying the bytes from the cache and device to the new slice.
//
// When we are implementing this we might want to just enforce the length to be the same as the block size.
func (o *Overlay) Slice(off, length int64) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (o *Overlay) WriteAt(p []byte, off int64) (int, error) {
	return o.cache.WriteAt(p, off)
}

func (o *Overlay) Size() (int64, error) {
	return o.cache.Size()
}

func (o *Overlay) BlockSize() int64 {
	return o.blockSize
}

func (o *Overlay) Close() error {
	if o.cacheEjected.Load() {
		return nil
	}

	return o.cache.Close()
}

func (o *Overlay) Header() *header.Header {
	return o.device.Header()
}

func (o *Overlay) CopyAllToCache() error {
	size, err := o.device.Size()
	if err != nil {
		return fmt.Errorf("error getting device size: %w", err)
	}

	for i := int64(0); i < size; i += o.blockSize {
		slice, err := o.device.Slice(i, o.blockSize)
		if err != nil {
			return fmt.Errorf("error getting device slice: %w", err)
		}

		if _, err := o.cache.WriteAt(slice, i); err != nil {
			return fmt.Errorf("error writing to cache: %w", err)
		}
	}

	return nil
}

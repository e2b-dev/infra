package block

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Overlay struct {
	device       ReadonlyDevice
	cache        *Cache
	cacheEjected atomic.Bool
	blockSize    int64
}

var _ Device = (*Overlay)(nil)

func NewOverlay(device ReadonlyDevice, cache *Cache) *Overlay {
	blockSize := device.BlockSize()

	return &Overlay{
		device:    device,
		cache:     cache,
		blockSize: blockSize,
	}
}

func (o *Overlay) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	blocks := header.BlocksOffsets(int64(len(p)), o.blockSize)

	for _, blockOff := range blocks {
		n, err := o.cache.ReadAt(p[blockOff:blockOff+o.blockSize], off+blockOff)
		if err == nil {
			continue
		}

		if !errors.As(err, &BytesNotAvailableError{}) {
			return n, fmt.Errorf("error reading from cache: %w", err)
		}

		n, err = o.device.ReadAt(ctx, p[blockOff:blockOff+o.blockSize], off+blockOff)
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
func (o *Overlay) Slice(_ context.Context, _, _ int64) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (o *Overlay) WriteAt(p []byte, off int64) (int, error) {
	return o.cache.WriteAt(p, off)
}

func (o *Overlay) Size(_ context.Context) (int64, error) {
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

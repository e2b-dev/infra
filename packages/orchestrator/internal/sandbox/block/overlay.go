package block

import (
	"errors"
	"fmt"
)

type Overlay struct {
	device ReadonlyDevice
	cache  *cache
}

func NewOverlay(device ReadonlyDevice, blockSize int64, cachePath string) (*Overlay, error) {
	size, err := device.Size()
	if err != nil {
		return nil, fmt.Errorf("error getting device size: %w", err)
	}

	cache, err := newCache(size, blockSize, cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	return &Overlay{
		device: device,
		cache:  cache,
	}, nil
}

func (o *Overlay) ReadAt(p []byte, off int64) (int, error) {
	blocks := listBlocks(off, off+int64(len(p)), o.cache.blockSize)

	for _, block := range blocks {
		n, err := o.cache.ReadAt(p[block.start-off:block.end-off], block.start)
		if err == nil {
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

func (o *Overlay) WriteAt(p []byte, off int64) (int, error) {
	return o.cache.WriteAt(p, off)
}

func (o *Overlay) Size() (int64, error) {
	return o.cache.Size()
}

func (o *Overlay) Close() error {
	return o.cache.Close()
}

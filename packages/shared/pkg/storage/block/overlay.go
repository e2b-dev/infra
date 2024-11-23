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
	n, err := o.cache.ReadAt(p, off)
	if err == nil {
		return n, nil
	}

	if !errors.As(err, &ErrBytesNotAvailable{}) {
		return n, fmt.Errorf("error reading from cache: %w", err)
	}

	n, err = o.device.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("error reading from device: %w", err)
	}

	return n, nil
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

package block

import (
	"errors"
	"fmt"
	"io"
)

type Overlay struct {
	base  io.ReaderAt
	cache Device
}

func newOverlay(base io.ReaderAt, cache Device) *Overlay {
	return &Overlay{
		base:  base,
		cache: cache,
	}
}

func (o *Overlay) WriteAt(p []byte, off int64) (int, error) {
	n, err := o.cache.WriteAt(p, off)
	if err != nil {
		return n, fmt.Errorf("error writing to cache: %w", err)
	}

	return n, nil
}

func (o *Overlay) ReadAt(b []byte, off int64) (int, error) {
	n, err := o.cache.ReadAt(b, off)
	if errors.As(err, &ErrBytesNotAvailable{}) {
		n, err = o.base.ReadAt(b, off)
		if err != nil {
			return n, fmt.Errorf("error reading from base: %w", err)
		}

		_, cacheErr := o.cache.WriteAt(b[:n], off)
		if cacheErr != nil {
			return n, fmt.Errorf("error writing to cache: %w", cacheErr)
		}
	}

	if err != nil {
		return n, fmt.Errorf("error reading from cache: %w", err)
	}

	return n, nil
}

func (o *Overlay) Sync() error {
	return o.cache.Sync()
}

func (o *Overlay) Size() (int64, error) {
	return o.cache.Size()
}

func (o *Overlay) Slice(offset, length int64) ([]byte, error) {
	return o.cache.Slice(offset, length)
}

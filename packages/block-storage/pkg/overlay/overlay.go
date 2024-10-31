package overlay

import (
	"errors"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
)

type Overlay struct {
	base         io.ReaderAt
	cache        block.Device
	writeToCache bool
}

func New(base io.ReaderAt, cache block.Device, writeToCache bool) *Overlay {
	return &Overlay{
		base:         base,
		cache:        cache,
		writeToCache: writeToCache,
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
	if errors.As(err, &block.ErrBytesNotAvailable{}) {
		n, err = o.base.ReadAt(b, off)
		if err != nil {
			return n, fmt.Errorf("error reading from base: %w", err)
		}

		if o.writeToCache {
			_, cacheErr := o.cache.WriteAt(b[:n], off)
			if cacheErr != nil {
				return n, fmt.Errorf("error writing to cache: %w", cacheErr)
			}
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

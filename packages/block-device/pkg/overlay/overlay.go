package overlay

import (
	"errors"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
)

type Overlay struct {
	base       io.ReaderAt
	cache      block.Device
	cacheReads bool
}

func New(base io.ReaderAt, cache block.Device, cacheReads bool) *Overlay {
	return &Overlay{
		base:       base,
		cache:      cache,
		cacheReads: cacheReads,
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

		if o.cacheReads {
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

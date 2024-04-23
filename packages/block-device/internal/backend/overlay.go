package backend

import (
	"errors"
	"io"

	"github.com/e2b-dev/infra/packages/block-device/internal/block"
)

type Overlay struct {
	base       io.ReaderAt
	cache      block.Device
	cacheReads bool
}

func NewOverlay(base io.ReaderAt, cache block.Device, cacheReads bool) *Overlay {
	return &Overlay{
		base:       base,
		cache:      cache,
		cacheReads: cacheReads,
	}
}

func (o *Overlay) WriteAt(p []byte, off int64) (int, error) {
	return o.cache.WriteAt(p, off)
}

func (o *Overlay) ReadAt(b []byte, off int64) (int, error) {
	n, err := o.cache.ReadAt(b, off)
	if errors.As(err, &block.ErrBytesNotAvailable{}) {
		n, err = o.base.ReadAt(b, off)
		if err != nil {
			return n, err
		}

		if o.cacheReads {
			n, err := o.cache.WriteAt(b[:n], off)
			if err != nil {
				return n, err
			}
		}
	}

	if err != nil {
		return n, err
	}

	return n, nil
}

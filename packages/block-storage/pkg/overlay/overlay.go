package overlay

import (
	"errors"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"
)

type Overlay struct {
	base  io.ReaderAt
	cache block.Device
}

func New(base io.ReaderAt, cache block.Device) *Overlay {
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
	if err == nil {
		return n, nil
	}

	if !errors.As(err, &block.ErrBytesNotAvailable{}) {
		return 0, fmt.Errorf("error reading cache: %w", err)
	}

	n, err = o.base.ReadAt(b, off)
	if err != nil {
		return n, fmt.Errorf("error reading from base: %w", err)
	}

	_, cacheErr := o.cache.WriteAt(b[:n], off)
	if cacheErr != nil {
		return n, fmt.Errorf("error writing to cache: %w", cacheErr)
	}

	return n, nil
}

func (o *Overlay) Sync() error {
	return o.cache.Sync()
}

func (o *Overlay) Size() (int64, error) {
	return o.cache.Size()
}

package pkg

import (
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/overlay"
)

type BucketOverlay struct {
	overlay *overlay.Overlay
	Close   func() error
	size    int64
}

func newBucketOverlay(base io.ReaderAt, cachePath string, size int64) (*BucketOverlay, error) {
	cache, err := cache.NewMmapCache(size, cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := overlay.New(base, cache, true)

	return &BucketOverlay{
		overlay: overlay,
		size:    size,

		Close: func() error {
			closeErr := cache.Close()
			if closeErr != nil {
				return fmt.Errorf("error closing cache: %w", closeErr)
			}

			return nil
		},
	}, nil
}

func (o *BucketOverlay) ReadAt(p []byte, off int64) (n int, err error) {
	return o.overlay.ReadAt(p, off)
}

func (o *BucketOverlay) WriteAt(p []byte, off int64) (n int, err error) {
	return o.overlay.WriteAt(p, off)
}

func (d *BucketOverlay) Size() int64 {
	return d.size
}

func (d *BucketOverlay) Sync() error {
	return d.overlay.Sync()
}

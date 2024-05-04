package pkg

import (
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/overlay"
)

type BucketObjectOverlay struct {
	overlay *overlay.Overlay
	Close   func() error
	size    int64
}

func newBucketObjectOverlay(base io.ReaderAt, cachePath string, size int64) (*BucketObjectOverlay, error) {
	cache, err := cache.NewMmapCache(size, cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := overlay.New(base, cache, true)

	return &BucketObjectOverlay{
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

func (o *BucketObjectOverlay) ReadAt(p []byte, off int64) (n int, err error) {
	return o.overlay.ReadAt(p, off)
}

func (o *BucketObjectOverlay) WriteAt(p []byte, off int64) (n int, err error) {
	return o.overlay.WriteAt(p, off)
}

func (d *BucketObjectOverlay) Size() int64 {
	return d.size
}

func (d *BucketObjectOverlay) Sync() error {
	return d.overlay.Sync()
}

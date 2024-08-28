package pkg

import (
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/overlay"
)

type BucketObjectOverlay struct {
	overlay   *overlay.Overlay
	cache     *cache.MmapCache
	size      int64
	blockSize int64
}

func newBucketObjectOverlay(base io.ReaderAt, cachePath string, size, blockSize int64) (*BucketObjectOverlay, error) {
	cache, err := cache.NewMmapCache(size, blockSize, cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := overlay.New(base, cache, true)

	return &BucketObjectOverlay{
		blockSize: blockSize,
		overlay:   overlay,
		size:      size,
		cache:     cache,
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

func (d *BucketObjectOverlay) Close() error {
	return d.cache.Close()
}

package pkg

import (
	"io"
	"os"

	"github.com/e2b-dev/infra/packages/block-device/pkg/backend"
)

type BucketOverlay struct {
	overlay *backend.Overlay
	cache   *backend.MmapCache
}

func newBucketOverlay(source io.ReaderAt, cachePath string, size int64) (*BucketOverlay, error) {
	cacheExists := false
	if _, err := os.Stat(cachePath); err == nil {
		cacheExists = true
	}

	cache, err := backend.NewMmapCache(size, cachePath, cacheExists)
	if err != nil {
		return nil, err
	}

	overlay := backend.NewOverlay(source, cache, true)

	return &BucketOverlay{
		overlay: overlay,
		cache:   cache,
	}, nil
}

func (o *BucketOverlay) ReadAt(p []byte, off int64) (n int, err error) {
	return o.overlay.ReadAt(p, off)
}

func (o *BucketOverlay) WriteAt(p []byte, off int64) (n int, err error) {
	return o.overlay.WriteAt(p, off)
}

func (o *BucketOverlay) Close() error {
	return o.cache.Close()
}

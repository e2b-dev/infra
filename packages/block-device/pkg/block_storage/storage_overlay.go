package block_storage

import (
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/overlay"
)

type BlockStorageOverlay struct {
	overlay   *overlay.Overlay
	cache     *cache.MmapCache
	size      int64
	blockSize int64
}

func newBlockStorageOverlay(base io.ReaderAt, cachePath string, size, blockSize int64) (*BlockStorageOverlay, error) {
	cache, err := cache.NewMmapCache(size, blockSize, cachePath)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	overlay := overlay.New(base, cache, true)

	return &BlockStorageOverlay{
		blockSize: blockSize,
		overlay:   overlay,
		size:      size,
		cache:     cache,
	}, nil
}

func (o *BlockStorageOverlay) ReadAt(p []byte, off int64) (n int, err error) {
	return o.overlay.ReadAt(p, off)
}

func (o *BlockStorageOverlay) WriteAt(p []byte, off int64) (n int, err error) {
	return o.overlay.WriteAt(p, off)
}

func (d *BlockStorageOverlay) Size() int64 {
	return d.size
}

func (d *BlockStorageOverlay) Sync() error {
	return d.overlay.Sync()
}

func (d *BlockStorageOverlay) Close() error {
	return d.cache.Close()
}

package pkg

import (
	"context"
	"errors"
	"os"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/source"
)

type BucketSource struct {
	base        *source.GCS
	cache       *cache.Mmap
	prefetecher *source.Prefetcher
	chunker     *source.Chunker

	size int64
}

func NewBucketSource(
	ctx context.Context,
	bucketName,
	bucketPath,
	bucketCachePath string,
	size int64,
) (*BucketSource, error) {
	cacheExists := false
	if _, err := os.Stat(bucketCachePath); err == nil {
		cacheExists = true
	}

	base, err := source.NewGCS(ctx, bucketName, bucketPath)
	if err != nil {
		return nil, err
	}

	cache, err := cache.NewMmapCache(size, bucketCachePath, cacheExists)
	if err != nil {
		return nil, err
	}

	chunker := source.NewChunker(ctx, base, cache)

	prefetcher := source.NewPrefetcher(ctx, chunker, size)
	go prefetcher.Start()

	return &BucketSource{
		base:        base,
		cache:       cache,
		prefetecher: prefetcher,
		chunker:     chunker,

		size: size,
	}, nil
}

func (d *BucketSource) ReadAt(p []byte, off int64) (n int, err error) {
	return d.chunker.ReadAt(p, off)
}

func (d *BucketSource) Close() error {
	d.prefetecher.Close()
	sourceErr := d.base.Close()
	cacheErr := d.cache.Close()

	return errors.Join(sourceErr, cacheErr)
}

func (d *BucketSource) CreateOverlay(cachePath string) (*BucketOverlay, error) {
	return newBucketOverlay(d.base, cachePath, d.size)
}

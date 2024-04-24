package pkg

import (
	"context"
	"errors"
	"os"

	"github.com/e2b-dev/infra/packages/block-device/pkg/backend"
)

type BucketSource struct {
	ctx    context.Context
	cancel context.CancelFunc

	source      *backend.GCS
	cache       *backend.MmapCache
	prefetecher *backend.Prefetcher
	chunker     *backend.ChunkerSyncer

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

	source, err := backend.NewGCS(
		ctx,
		bucketName,
		bucketPath,
	)
	if err != nil {
		return nil, err
	}

	cache, err := backend.NewMmapCache(size, bucketCachePath, cacheExists)
	if err != nil {
		return nil, err
	}

	chunker := backend.NewChunkerSyncer(source, cache)

	prefetcher := backend.NewPrefetcher(cache, size)
	go prefetcher.Start()

	return &BucketSource{
		source:      source,
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
	d.cancel()

	d.prefetecher.Close()
	sourceErr := d.source.Close()
	cacheErr := d.cache.Close()

	return errors.Join(sourceErr, cacheErr)
}

func (d *BucketSource) CreateOverlay(cachePath string) (*BucketOverlay, error) {
	return newBucketOverlay(d.source, cachePath, d.size)
}

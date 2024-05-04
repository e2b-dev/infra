package pkg

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/source"

	"cloud.google.com/go/storage"
)

type BucketObjectSource struct {
	retrier    *source.Retrier
	chunker    *source.Chunker
	prefetcher *source.Prefetcher
	cache      *cache.MmapCache
	size       int64
}

const (
	bucketFetchRetries    = 3
	bucketFetchRetryDelay = 1 * time.Millisecond
)

func NewBucketObjectSource(
	ctx context.Context,
	client *storage.Client,
	bucketName,
	bucketPath,
	bucketCachePath string,
) (*BucketObjectSource, error) {
	object := source.NewGCSObject(ctx, client, bucketName, bucketPath)

	size, err := object.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get object size: %w", err)
	}

	retrier := source.NewRetrier(ctx, object, bucketFetchRetries, bucketFetchRetryDelay)

	cache, err := cache.NewMmapCache(size, bucketCachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket cache: %w", err)
	}

	chunker := source.NewChunker(ctx, retrier, cache)

	prefetcher := source.NewPrefetcher(ctx, chunker, size)
	go func() {
		prefetchErr := prefetcher.Start()
		if prefetchErr != nil {
			log.Printf("error prefetching chunks: %v", prefetchErr)
		}
	}()

	return &BucketObjectSource{
		size:       size,
		retrier:    retrier,
		chunker:    chunker,
		prefetcher: prefetcher,
		cache:      cache,
	}, nil
}

func (d *BucketObjectSource) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.chunker.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	return n, nil
}

func (d *BucketObjectSource) CreateOverlay(cachePath string) (*BucketObjectOverlay, error) {
	overlay, err := newBucketObjectOverlay(d.chunker, cachePath, d.size)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket overlay: %w", err)
	}

	return overlay, nil
}

func (d *BucketObjectSource) Size() int64 {
	return d.size
}

func (d *BucketObjectSource) Close() error {
	d.prefetcher.Close()
	d.retrier.Close()
	d.chunker.Close()

	return d.cache.Close()
}

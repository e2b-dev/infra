package pkg

import (
	"context"
	"fmt"
	"log"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/source"

	"cloud.google.com/go/storage"
)

type BucketObjectSource struct {
	source *source.Chunker
	cache  *cache.MmapCache
	size   int64
}



func NewBucketObjectSource(
	ctx context.Context,
	client *storage.Client,
	bucket,
	bucketObjectPath,
	cachePath string,
) (*BucketObjectSource, error) {
	object := source.NewGCSObject(ctx, client, bucket, bucketObjectPath)

	size, err := object.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get object size: %w", err)
	}

	retrier := source.NewRetrier(ctx, object, source.FetchRetries, source.FetchRetryDelay)

	cache, err := cache.NewMmapCache(size, cachePath)
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
		size:   size,
		source: chunker,
		cache:  cache,
	}, nil
}

func (d *BucketObjectSource) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.source.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	return n, nil
}

func (d *BucketObjectSource) CreateOverlay(cachePath string) (*BucketObjectOverlay, error) {
	overlay, err := newBucketObjectOverlay(d.source, cachePath, d.size)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket overlay: %w", err)
	}

	return overlay, nil
}

func (d *BucketObjectSource) Size() int64 {
	return d.size
}

func (d *BucketObjectSource) Close() error {
	return d.cache.Close()
}

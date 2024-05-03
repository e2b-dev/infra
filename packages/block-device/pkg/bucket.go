package pkg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/e2b-dev/infra/packages/block-device/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-device/pkg/source"
)

type BucketSource struct {
	bucketReader io.ReaderAt
	Close        func() error
	size         int64
}

const (
	bucketFetchRetries    = 3
	bucketFetchRetryDelay = 1 * time.Millisecond
)

func NewBucketSource(
	ctx context.Context,
	bucketName,
	bucketPath,
	bucketCachePath string,
	size int64,
) (*BucketSource, error) {
	bucket, err := source.NewGCS(ctx, bucketName, bucketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket source: %w", err)
	}

	retrier := source.NewRetrier(ctx, bucket, bucketFetchRetries, bucketFetchRetryDelay)

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

	return &BucketSource{
		bucketReader: chunker,
		size:         size,

		Close: func() error {
			prefetcher.Close()
			retrier.Close()
			chunker.Close()

			bucketErr := bucket.Close()
			cacheErr := cache.Close()

			return errors.Join(bucketErr, cacheErr)
		},
	}, nil
}

func (d *BucketSource) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.bucketReader.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	return n, nil
}

func (d *BucketSource) CreateOverlay(cachePath string) (*BucketOverlay, error) {
	overlay, err := newBucketOverlay(d.bucketReader, cachePath, d.size)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket overlay: %w", err)
	}

	return overlay, nil
}

func (d *BucketSource) Size() int64 {
	return d.size
}

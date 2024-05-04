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

	"cloud.google.com/go/storage"
)

type BucketObjectSource struct {
	reader io.ReaderAt
	Close  func() error
	size   int64
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
	object, err := source.NewGCSObject(ctx, client, bucketName, bucketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket source: %w", err)
	}

	size, err := object.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket size: %w", err)
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
		reader: chunker,
		size:   size,

		Close: func() error {
			prefetcher.Close()
			retrier.Close()
			chunker.Close()

			objectErr := object.Close()
			cacheErr := cache.Close()

			return errors.Join(objectErr, cacheErr)
		},
	}, nil
}

func (d *BucketObjectSource) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.reader.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	return n, nil
}

func (d *BucketObjectSource) CreateOverlay(cachePath string) (*BucketObjectOverlay, error) {
	overlay, err := newBucketObjectOverlay(d.reader, cachePath, d.size)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket overlay: %w", err)
	}

	return overlay, nil
}

func (d *BucketObjectSource) Size() int64 {
	return d.size
}

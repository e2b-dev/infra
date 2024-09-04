package block_storage

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/cache"
	"github.com/e2b-dev/infra/packages/block-storage/pkg/source"

	"cloud.google.com/go/storage"
)

type BlockStorage struct {
	source    *source.Chunker
	cache     *cache.MmapCache
	size      int64
	blockSize int64
}

type StorageObject interface {
	io.ReaderAt
	Size() (int64, error)
}

func NewBucketObject(
	ctx context.Context,
	client *storage.Client,
	bucket string,
	bucketObjectPath string,
) StorageObject {
	return source.NewGCSObject(ctx, client, bucket, bucketObjectPath)
}

func New(
	ctx context.Context,
	object StorageObject,
	cachePath string,
	blockSize int64,
) (*BlockStorage, error) {
	size, err := object.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get storage object size: %w", err)
	}

	cache, err := cache.NewMmapCache(size, blockSize, cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket cache: %w", err)
	}

	chunker := source.NewChunker(ctx, object, cache)

	prefetcher := source.NewPrefetcher(ctx, chunker, size)
	go func() {
		prefetchErr := prefetcher.Start()
		if prefetchErr != nil {
			log.Printf("error prefetching chunks: %v", prefetchErr)
		}
	}()

	return &BlockStorage{
		size:      size,
		blockSize: blockSize,
		source:    chunker,
		cache:     cache,
	}, nil
}

func (d *BlockStorage) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.source.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	return n, nil
}

func (d *BlockStorage) CreateOverlay(cachePath string) (*BlockStorageOverlay, error) {
	overlay, err := newBlockStorageOverlay(d.source, cachePath, d.size, d.blockSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket overlay: %w", err)
	}

	return overlay, nil
}

func (d *BlockStorage) Size() int64 {
	return d.size
}

func (d *BlockStorage) Close() error {
	return d.cache.Close()
}

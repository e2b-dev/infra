package block

import (
	"context"
	"errors"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type Storage struct {
	source     *Chunker
	cache      *MmapCache
	blockSize  int64
	size       int64
	fetchGroup singleflight.Group
}

func NewStorage(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketObjectPath string,
	blockSize int64,
	cachePath string,
) (*Storage, error) {
	object := gcs.NewObjectFromBucket(ctx, bucket, bucketObjectPath)

	size, err := object.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get object size: %w", err)
	}

	cache, err := NewMmapCache(size, blockSize, cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	chunker := NewChunker(ctx, size, object, cache)

	return &Storage{
		blockSize: blockSize,
		source:    chunker,
		size:      size,
		cache:     cache,
	}, nil
}

//
// -> read request that is multiple of block size
// -> return if cow cache has it (use file or mmap cache)
// -> check if the local cache has it (use file or mmap cache, try exposing readRaw for uffd performance, also try to not lock the whole files)
// -> if not read from source but read in Chunk size and deduplicate the request from the source by chunks
// -> after returning from source, write to local cache
//

// TODO: Ensure that the maximum size of the buffer is the block size or handle if it is bigger.
func (d *Storage) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.source.ReadAt(p, off)

	if err == nil || errors.Is(err, io.EOF) {
		return n, nil
	}

	return 0, err
}

func (d *Storage) Size() (int64, error) {
	return d.size, nil
}

func (d *Storage) Close() error {
	err := d.cache.Close()
	if err != nil {
		return fmt.Errorf("failed to close cache file: %w", err)
	}

	return nil
}

func (d *Storage) Slice(off, length int64) ([]byte, error) {
	return d.source.Slice(off, length)
}

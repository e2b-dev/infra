package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"cloud.google.com/go/storage"
	"golang.org/x/sync/singleflight"
)

type BlockStorage struct {
	source     *GCSObject
	cache      *FileCache
	blockSize  int64
	size       int64
	fetchGroup singleflight.Group
}

func NewBlockStorage(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketObjectPath string,
	blockSize int64,
	cachePath string,
) (*BlockStorage, error) {
	object := NewGCSObjectFromBucket(ctx, bucket, bucketObjectPath)

	size, err := object.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get object size: %w", err)
	}

	cache, err := NewFileCache(size, blockSize, cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}

	return &BlockStorage{
		blockSize: blockSize,
		source:    object,
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
func (d *BlockStorage) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.cache.ReadAt(p, off)
	if err == nil || errors.Is(err, io.EOF) {
		return n, nil
	}

	if !errors.As(err, &ErrBytesNotAvailable{}) {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	_, err, _ = d.fetchGroup.Do(strconv.FormatUint(uint64(off), 10), func() (interface{}, error) {
		if d.cache.IsCached(off) {
			return nil, nil
		}

		buf := make([]byte, d.blockSize)

		n, err = d.source.ReadAt(buf, off)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("failed to read from source %d: %w", off, err)
		}

		_, err = d.cache.WriteAt(buf, off)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("failed to write to cache %d: %w", off, err)
		}

		return nil, nil
	})

	n, err = d.cache.ReadAt(p, off)
	if err == nil || errors.Is(err, io.EOF) {
		return n, nil
	}

	return 0, err
}

func (d *BlockStorage) Size() (int64, error) {
	return d.size, nil
}

func (d *BlockStorage) Close() error {
	err := d.cache.Close()
	if err != nil {
		return fmt.Errorf("failed to close cache file: %w", err)
	}

	return nil
}

func (d *BlockStorage) Sync() error {
	return d.cache.Sync()
}

// Not supported
func (d *BlockStorage) WriteAt(p []byte, off int64) (n int, err error) {
	fmt.Fprintf(os.Stderr, "block storage write at not supported %s\n", d.source.object.ObjectName())

	return 0, nil
}

func (d *BlockStorage) BlockSize() int64 {
	return d.blockSize
}

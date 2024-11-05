package storage

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"cloud.google.com/go/storage"
)

type BlockStorage struct {
	source    *GCSObject
	cache     *FileCache
	blockSize int64
	size      int64
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

func (d *BlockStorage) ReadAt(p []byte, off int64) (n int, err error) {
	log.Printf("[%s] Reading %d at %d\n", strings.Split(d.source.object.ObjectName(), "/")[2], len(p), off)

	n, err = d.cache.ReadAt(p, off)
	if err == nil {
		log.Printf("[%s] Read %d at %d\n", strings.Split(d.source.object.ObjectName(), "/")[2], len(p), off)
		return n, nil
	}

	if errors.As(err, &ErrBytesNotAvailable{}) {
		n, err = d.source.ReadAt(p, off)
		if err != nil {
			return n, fmt.Errorf("failed to read %d: %w", off, err)
		}

		_, err = d.cache.WriteAt(p, off)
		if err != nil {
			return n, fmt.Errorf("failed to write %d: %w", off, err)
		}
	}

	log.Printf("[%s] Read %d at %d\n", strings.Split(d.source.object.ObjectName(), "/")[2], len(p), off)

	return n, nil
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

package storage

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
)

type BlockStorage struct {
	source    *GCSObject
	blockSize int64
}

func NewBlockStorage(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketObjectPath string,
	blockSize int64,
) *BlockStorage {
	object := NewGCSObjectFromBucket(ctx, bucket, bucketObjectPath)

	return &BlockStorage{
		blockSize: blockSize,
		source:    object,
	}
}

func (d *BlockStorage) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.source.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	return n, nil
}

func (d *BlockStorage) Size() (int64, error) {
	return d.source.Size()
}

func (d *BlockStorage) Close() error {
	return nil
}

func (d *BlockStorage) Sync() error {
	return nil
}

func (d *BlockStorage) WriteAt(p []byte, off int64) (n int, err error) {
	// Not supported
	return 0, nil
}

func (d *BlockStorage) BlockSize() int64 {
	return d.blockSize
}

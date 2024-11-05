package storage

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/storage"
)

type BlockStorage struct {
	source    *GCSObject
	blockSize int64
	size      int64
}

func NewBlockStorage(
	ctx context.Context,
	bucket *storage.BucketHandle,
	bucketObjectPath string,
	blockSize int64,
) (*BlockStorage, error) {
	object := NewGCSObjectFromBucket(ctx, bucket, bucketObjectPath)

	size, err := object.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get object size: %w", err)
	}

	return &BlockStorage{
		blockSize: blockSize,
		source:    object,
		size:      size,
	}, nil
}

func (d *BlockStorage) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.source.ReadAt(p, off)
	if err != nil {
		return n, fmt.Errorf("failed to read %d: %w", off, err)
	}

	return n, nil
}

func (d *BlockStorage) Size() (int64, error) {
	return d.size, nil
}

func (d *BlockStorage) Close() error {
	return nil
}

func (d *BlockStorage) Sync() error {
	return nil
}

// Not supported
func (d *BlockStorage) WriteAt(p []byte, off int64) (n int, err error) {
	fmt.Fprintf(os.Stderr, "block storage write at not supported %s\n", d.source.object.ObjectName())

	return 0, nil
}

func (d *BlockStorage) BlockSize() int64 {
	return d.blockSize
}

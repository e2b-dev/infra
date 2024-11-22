package block

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type Storage struct {
	source *chunker
	size   int64
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

	chunker, err := newChunker(ctx, size, blockSize, object, cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunker: %w", err)
	}

	return &Storage{
		source: chunker,
		size:   size,
	}, nil
}

func (d *Storage) ReadAt(p []byte, off int64) (int, error) {
	return d.source.ReadAt(p, off)
}

func (d *Storage) Size() (int64, error) {
	return d.size, nil
}

func (d *Storage) Close() error {
	return d.source.Close()
}

func (d *Storage) Slice(off, length int64) ([]byte, error) {
	return d.source.Slice(off, length)
}

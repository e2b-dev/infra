package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/google/uuid"
)

type StorageDiff struct {
	chunker   *block.Chunker
	size      int64
	blockSize int64
	ctx       context.Context
	bucket    *gcs.BucketHandle
	id        string
}

func newStorageDiff(
	ctx context.Context,
	bucket *gcs.BucketHandle,
	id string,
	blockSize int64,
) *StorageDiff {
	return &StorageDiff{
		blockSize: blockSize,
		ctx:       ctx,
		bucket:    bucket,
		id:        id,
	}
}

func (b *StorageDiff) Init() error {
	obj := gcs.NewObject(b.ctx, b.bucket, b.id)

	size, err := obj.Size()
	if err != nil {
		return fmt.Errorf("failed to get object size: %w", err)
	}

	id := uuid.New()

	chunker, err := block.NewChunker(b.ctx, size, b.blockSize, obj, filepath.Join(cachePath, id.String()))
	if err != nil {
		return fmt.Errorf("failed to create chunker: %w", err)
	}

	b.chunker = chunker

	return nil
}

func (b *StorageDiff) Close() error {
	return b.chunker.Close()
}

func (b *StorageDiff) ReadAt(p []byte, off int64) (int, error) {
	return b.chunker.ReadAt(p, off)
}

func (b *StorageDiff) Size() (int64, error) {
	return b.size, nil
}

func (b *StorageDiff) Slice(off, length int64) ([]byte, error) {
	return b.chunker.Slice(off, length)
}

func (b *StorageDiff) WriteTo(w io.Writer) (int64, error) {
	return b.chunker.WriteTo(w)
}

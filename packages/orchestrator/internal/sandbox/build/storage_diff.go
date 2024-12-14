package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

func storagePath(buildId string, diffType DiffType) string {
	return fmt.Sprintf("%s/%s", buildId, diffType)
}

type StorageDiff struct {
	chunker     *block.Chunker
	size        int64
	blockSize   int64
	ctx         context.Context
	bucket      *gcs.BucketHandle
	storagePath string
	cachePath   string
}

func newStorageDiff(
	ctx context.Context,
	bucket *gcs.BucketHandle,
	buildId string,
	diffType DiffType,
	blockSize int64,
) *StorageDiff {
	cachePathSuffix := id.Generate()

	storagePath := storagePath(buildId, diffType)
	cacheFile := fmt.Sprintf("%s-%s-%s", buildId, diffType, cachePathSuffix)
	cachePath := filepath.Join(cachePath, cacheFile)

	return &StorageDiff{
		blockSize:   blockSize,
		ctx:         ctx,
		bucket:      bucket,
		storagePath: storagePath,
		cachePath:   cachePath,
	}
}

func (b *StorageDiff) CacheKey() string {
	return b.storagePath
}

func (b *StorageDiff) Init() error {
	obj := gcs.NewObject(b.ctx, b.bucket, b.storagePath)

	size, err := obj.Size()
	if err != nil {
		return fmt.Errorf("failed to get object size: %w", err)
	}

	chunker, err := block.NewChunker(b.ctx, size, b.blockSize, obj, b.cachePath)
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

// The local file might not be synced.
func (b *StorageDiff) Path() (string, error) {
	return b.cachePath, nil
}

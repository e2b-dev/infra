package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func storagePath(buildId string, diffType DiffType) string {
	return fmt.Sprintf("%s/%s", buildId, diffType)
}

type StorageDiff struct {
	chunker     *utils.SetOnce[*block.Chunker]
	cachePath   string
	cacheKey    DiffStoreKey
	storagePath string
	blockSize   int64
	bucket      *gcs.BucketHandle
}

func newStorageDiff(
	basePath string,
	buildId string,
	diffType DiffType,
	blockSize int64,
	bucket *gcs.BucketHandle,
) *StorageDiff {
	cachePathSuffix := id.Generate()

	storagePath := storagePath(buildId, diffType)
	cacheFile := fmt.Sprintf("%s-%s-%s", buildId, diffType, cachePathSuffix)
	cachePath := filepath.Join(basePath, cacheFile)

	return &StorageDiff{
		storagePath: storagePath,
		cachePath:   cachePath,
		chunker:     utils.NewSetOnce[*block.Chunker](),
		blockSize:   blockSize,
		bucket:      bucket,
		cacheKey:    GetDiffStoreKey(buildId, diffType),
	}
}

func (b *StorageDiff) CacheKey() DiffStoreKey {
	return b.cacheKey
}

func (b *StorageDiff) Init(ctx context.Context) error {
	obj := gcs.NewObject(ctx, b.bucket, b.storagePath)

	size, err := obj.Size()
	if err != nil {
		errMsg := fmt.Errorf("failed to get object size: %w", err)

		b.chunker.SetError(errMsg)

		return errMsg
	}

	chunker, err := block.NewChunker(ctx, size, b.blockSize, obj, b.cachePath)
	if err != nil {
		errMsg := fmt.Errorf("failed to create chunker: %w", err)

		b.chunker.SetError(errMsg)

		return errMsg
	}

	return b.chunker.SetValue(chunker)
}

func (b *StorageDiff) Close() error {
	c, err := b.chunker.Wait()
	if err != nil {
		return err
	}

	return c.Close()
}

func (b *StorageDiff) ReadAt(p []byte, off int64) (int, error) {
	c, err := b.chunker.Wait()
	if err != nil {
		return 0, err
	}

	return c.ReadAt(p, off)
}

func (b *StorageDiff) Slice(off, length int64) ([]byte, error) {
	c, err := b.chunker.Wait()
	if err != nil {
		return nil, err
	}

	return c.Slice(off, length)
}

func (b *StorageDiff) WriteTo(w io.Writer) (int64, error) {
	c, err := b.chunker.Wait()
	if err != nil {
		return 0, err
	}

	return c.WriteTo(w)
}

// The local file might not be synced.
func (b *StorageDiff) CachePath() (string, error) {
	return b.cachePath, nil
}

func (b *StorageDiff) FileSize() (int64, error) {
	c, err := b.chunker.Wait()
	if err != nil {
		return 0, err
	}

	return c.FileSize()
}

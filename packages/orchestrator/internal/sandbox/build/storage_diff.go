package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
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
	metrics     blockmetrics.Metrics
	persistence storage.StorageProvider
}

func newStorageDiff(
	basePath string,
	buildId string,
	diffType DiffType,
	blockSize int64,
	metrics blockmetrics.Metrics,
	persistence storage.StorageProvider,
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
		metrics:     metrics,
		persistence: persistence,
		cacheKey:    GetDiffStoreKey(buildId, diffType),
	}
}

func (b *StorageDiff) CacheKey() DiffStoreKey {
	return b.cacheKey
}

func (b *StorageDiff) Init(ctx context.Context) error {
	obj, err := b.persistence.OpenObject(ctx, b.storagePath)
	if err != nil {
		return err
	}

	size, err := obj.Size(ctx)
	if err != nil {
		errMsg := fmt.Errorf("failed to get object size: %w", err)
		b.chunker.SetError(errMsg)
		return errMsg
	}

	chunker, err := block.NewChunker(size, b.blockSize, obj, b.cachePath, b.metrics)
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

func (b *StorageDiff) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	c, err := b.chunker.Wait()
	if err != nil {
		return 0, err
	}

	return c.ReadAt(ctx, p, off)
}

func (b *StorageDiff) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	c, err := b.chunker.Wait()
	if err != nil {
		return nil, err
	}

	return c.Slice(ctx, off, length)
}

func (b *StorageDiff) WriteTo(ctx context.Context, w io.Writer) (int64, error) {
	c, err := b.chunker.Wait()
	if err != nil {
		return 0, err
	}

	return c.WriteTo(ctx, w)
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

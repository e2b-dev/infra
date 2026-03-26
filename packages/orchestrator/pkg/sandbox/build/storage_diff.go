package build

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// StoragePath returns the GCS path for a build's data file (without compression suffix).
func StoragePath(buildId string, diffType DiffType) string {
	return fmt.Sprintf("%s/%s", buildId, diffType)
}

type StorageDiff struct {
	chunker   *utils.SetOnce[*block.Chunker]
	cachePath string
	cacheKey  DiffStoreKey
	buildID   string
	diffType  DiffType

	blockSize        int64
	metrics          blockmetrics.Metrics
	persistence      storage.StorageProvider
	uncompressedSize int64 // 0 means unknown (fall back to Size() call)
}

var _ Diff = (*StorageDiff)(nil)

type UnknownDiffTypeError struct {
	DiffType DiffType
}

func (e UnknownDiffTypeError) Error() string {
	return fmt.Sprintf("unknown diff type: %s", e.DiffType)
}

func newStorageDiff(
	basePath string,
	buildId string,
	diffType DiffType,
	blockSize int64,
	metrics blockmetrics.Metrics,
	persistence storage.StorageProvider,
	uncompressedSize int64,
) (*StorageDiff, error) {
	if !isKnownDiffType(diffType) {
		return nil, UnknownDiffTypeError{diffType}
	}

	return &StorageDiff{
		buildID:          buildId,
		diffType:         diffType,
		cachePath:        GenerateDiffCachePath(basePath, buildId, diffType),
		chunker:          utils.NewSetOnce[*block.Chunker](),
		blockSize:        blockSize,
		metrics:          metrics,
		persistence:      persistence,
		uncompressedSize: uncompressedSize,
		cacheKey:         GetDiffStoreKey(buildId, diffType),
	}, nil
}

func isKnownDiffType(diffType DiffType) bool {
	return diffType == Memfile || diffType == Rootfs
}

func (b *StorageDiff) CacheKey() DiffStoreKey {
	return b.cacheKey
}

func (b *StorageDiff) Init(ctx context.Context) error {
	chunker, err := b.createChunker(ctx)
	if err != nil {
		errMsg := fmt.Errorf("failed to create chunker: %w", err)
		b.chunker.SetError(errMsg)

		return errMsg
	}

	return b.chunker.SetValue(chunker)
}

// createChunker resolves the uncompressed file size and creates a Chunker.
// For V3 builds (uncompressedSize == 0), falls back to a Size() network call on the
// base (uncompressed) path — V3 builds are always uncompressed.
func (b *StorageDiff) createChunker(ctx context.Context) (*block.Chunker, error) {
	size := b.uncompressedSize
	if size == 0 {
		basePath := StoragePath(b.buildID, b.diffType)
		obj, err := b.persistence.OpenFramedFile(ctx, basePath)
		if err != nil {
			return nil, fmt.Errorf("open asset %s: %w", basePath, err)
		}

		size, err = obj.Size(ctx)
		if err != nil {
			return nil, fmt.Errorf("get size of asset %s: %w", basePath, err)
		}
	}

	if size == 0 {
		return nil, fmt.Errorf("no asset found for %s/%s (size is 0)", b.buildID, b.diffType)
	}

	return block.NewChunker(b.buildID, string(b.diffType), b.persistence, size, b.blockSize, b.cachePath, b.metrics)
}

func (b *StorageDiff) Close() error {
	c, err := b.chunker.Wait()
	if err != nil {
		return err
	}

	return c.Close()
}

func (b *StorageDiff) ReadBlock(ctx context.Context, p []byte, off int64, ft *storage.FrameTable) (int, error) {
	chunker, err := b.chunker.Wait()
	if err != nil {
		return 0, err
	}

	return chunker.ReadBlock(ctx, p, off, ft)
}

func (b *StorageDiff) SliceBlock(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	chunker, err := b.chunker.Wait()
	if err != nil {
		return nil, err
	}

	return chunker.SliceBlock(ctx, off, length, ft)
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

func (b *StorageDiff) BlockSize() int64 {
	return b.blockSize
}

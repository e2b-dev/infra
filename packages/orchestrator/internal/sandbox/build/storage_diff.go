package build

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
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

	blockSize       int64
	metrics         blockmetrics.Metrics
	persistence     storage.StorageProvider
	compressionType storage.CompressionType
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
	ct storage.CompressionType,
) (*StorageDiff, error) {
	storagePath := storagePath(buildId, diffType)
	if !isKnownDiffType(diffType) {
		return nil, UnknownDiffTypeError{diffType}
	}

	cachePath := GenerateDiffCachePath(basePath, buildId, diffType)

	return &StorageDiff{
		storagePath:     storagePath,
		cachePath:       cachePath,
		chunker:         utils.NewSetOnce[*block.Chunker](),
		blockSize:       blockSize,
		metrics:         metrics,
		persistence:     persistence,
		compressionType: ct,
		cacheKey:        GetDiffStoreKey(buildId, diffType),
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

// createChunker opens the single data file and creates a Chunker.
func (b *StorageDiff) createChunker(ctx context.Context) (*block.Chunker, error) {
	file, size, err := b.openDataFile(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open data file for %s: %w", b.storagePath, err)
	}

	if size == 0 {
		return nil, fmt.Errorf("no asset found for %s (size is 0)", b.storagePath)
	}

	compressed := b.compressionType != storage.CompressionNone

	return block.NewChunker(file, size, compressed, b.blockSize, b.cachePath, b.metrics)
}

// openDataFile opens the single data file based on compressionType.
// For uncompressed builds, opens the raw file (e.g. "buildId/memfile").
// For compressed builds, opens the compressed variant (e.g. "buildId/memfile.zstd").
//
// The returned size is always the uncompressed diff file size (not the full
// virtual address space, and not the compressed object size). For compressed
// objects, Size() reads this from the MetadataKeyUncompressedSize object metadata
// that was set during upload.
func (b *StorageDiff) openDataFile(ctx context.Context) (storage.FramedFile, int64, error) {
	path := b.storagePath
	if b.compressionType != storage.CompressionNone {
		path = storage.CompressedPath(b.storagePath, b.compressionType)
	}

	obj, err := b.persistence.OpenFramedFile(ctx, path)
	if err != nil {
		return nil, 0, fmt.Errorf("open asset %s: %w", path, err)
	}

	size, err := obj.Size(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get size of asset %s: %w", path, err)
	}

	return obj, size, nil
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

func (b *StorageDiff) GetBlock(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	chunker, err := b.chunker.Wait()
	if err != nil {
		return nil, err
	}

	return chunker.GetBlock(ctx, off, length, ft)
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

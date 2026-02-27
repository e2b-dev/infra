package build

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
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

	blockSize    int64
	metrics      blockmetrics.Metrics
	persistence  storage.StorageProvider
	featureFlags *featureflags.Client
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
	featureFlags *featureflags.Client,
) (*StorageDiff, error) {
	storagePath := storagePath(buildId, diffType)
	if !isKnownDiffType(diffType) {
		return nil, UnknownDiffTypeError{diffType}
	}

	cachePath := GenerateDiffCachePath(basePath, buildId, diffType)

	return &StorageDiff{
		storagePath:  storagePath,
		cachePath:    cachePath,
		chunker:      utils.NewSetOnce[*block.Chunker](),
		blockSize:    blockSize,
		metrics:      metrics,
		persistence:  persistence,
		featureFlags: featureFlags,
		cacheKey:     GetDiffStoreKey(buildId, diffType),
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

// createChunker probes for available assets and creates a Chunker.
func (b *StorageDiff) createChunker(ctx context.Context) (*block.Chunker, error) {
	assets := b.probeAssets(ctx)
	if assets.Size == 0 {
		return nil, fmt.Errorf("no asset found for %s (no uncompressed or compressed with metadata)", b.storagePath)
	}

	return block.NewChunker(assets, b.blockSize, b.cachePath, b.metrics, b.featureFlags)
}

// probeAssets probes for uncompressed and compressed asset variants in parallel.
// For compressed objects, Size() returns the uncompressed size from metadata,
// allowing us to derive the mmap allocation size even when the uncompressed
// object doesn't exist.
func (b *StorageDiff) probeAssets(ctx context.Context) block.AssetInfo {
	assets := block.AssetInfo{BasePath: b.storagePath}

	var (
		lz4UncompressedSize  int64
		zstdUncompressedSize int64
	)

	// Probe all 3 paths in parallel: uncompressed, v4.*.lz4, v4.*.zstd.
	// Errors are swallowed (missing assets are expected).
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		obj, err := b.persistence.OpenFramedFile(ctx, b.storagePath)
		if err != nil {
			return nil //nolint:nilerr // missing asset is expected
		}

		uncompressedSize, err := obj.Size(ctx)
		if err != nil {
			return nil //nolint:nilerr // missing asset is expected
		}

		assets.Size = uncompressedSize
		assets.HasUncompressed = true
		assets.Uncompressed = obj

		return nil
	})

	eg.Go(func() error {
		lz4Path := storage.V4DataPath(b.storagePath, storage.CompressionLZ4)
		obj, err := b.persistence.OpenFramedFile(ctx, lz4Path)
		if err != nil {
			return nil //nolint:nilerr // missing asset is expected
		}

		uncompressedSize, err := obj.Size(ctx)
		if err != nil {
			return nil //nolint:nilerr // missing asset is expected
		}

		assets.HasLZ4 = true
		assets.LZ4 = obj
		lz4UncompressedSize = uncompressedSize

		return nil
	})

	eg.Go(func() error {
		zstdPath := storage.V4DataPath(b.storagePath, storage.CompressionZstd)
		obj, err := b.persistence.OpenFramedFile(ctx, zstdPath)
		if err != nil {
			return nil //nolint:nilerr // missing asset is expected
		}

		uncompressedSize, err := obj.Size(ctx)
		if err != nil {
			return nil //nolint:nilerr // missing asset is expected
		}

		assets.HasZstd = true
		assets.Zstd = obj
		zstdUncompressedSize = uncompressedSize

		return nil
	})

	_ = eg.Wait()

	// If no uncompressed object exists, derive the mmap allocation size
	// from the compressed object's uncompressed-size metadata.
	if assets.Size == 0 {
		if lz4UncompressedSize > 0 {
			assets.Size = lz4UncompressedSize
		} else if zstdUncompressedSize > 0 {
			assets.Size = zstdUncompressedSize
		}
	}

	return assets
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

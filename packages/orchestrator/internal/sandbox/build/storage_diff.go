package build

import (
	"context"
	"fmt"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func storagePath(buildId string, diffType DiffType) string {
	return fmt.Sprintf("%s/%s", buildId, diffType)
}

// StorageDiff represents a build's file (memfile or rootfs) backed by cloud storage.
// The chunker is lazily initialized on first read using the frame table from the mapping.
type StorageDiff struct {
	// chunker is lazily initialized via chunkerOnce on first ReadAt/Slice call.
	chunker     block.Chunker
	chunkerOnce sync.Once
	chunkerErr  error

	cachePath string
	cacheKey  DiffStoreKey

	blockSize int64
	metrics   blockmetrics.Metrics

	objectPath  string
	persistence storage.StorageProvider
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
) (*StorageDiff, error) {
	objectPath := storagePath(buildId, diffType)
	_, ok := storageObjectType(diffType)
	if !ok {
		return nil, UnknownDiffTypeError{diffType}
	}

	cachePath := GenerateDiffCachePath(basePath, buildId, diffType)
	cacheKey := GetDiffStoreKey(buildId, diffType)

	return &StorageDiff{
		objectPath:  objectPath,
		cachePath:   cachePath,
		blockSize:   blockSize,
		metrics:     metrics,
		persistence: persistence,
		cacheKey:    cacheKey,
	}, nil
}

func storageObjectType(diffType DiffType) (storage.SeekableObjectType, bool) {
	switch diffType {
	case Memfile:
		return storage.MemfileObjectType, true
	case Rootfs:
		return storage.RootFSObjectType, true
	default:
		return storage.UnknownSeekableObjectType, false
	}
}

func (b *StorageDiff) CacheKey() DiffStoreKey {
	return b.cacheKey
}

// getChunker lazily initializes and returns the chunker.
// The frame table determines whether to use compressed or uncompressed chunker.
func (b *StorageDiff) getChunker(ctx context.Context, ft *storage.FrameTable) (block.Chunker, error) {
	b.chunkerOnce.Do(func() {
		b.chunker, b.chunkerErr = b.createChunker(ctx, ft)
	})

	return b.chunker, b.chunkerErr
}

// createChunker creates the appropriate chunker based on the frame table.
func (b *StorageDiff) createChunker(ctx context.Context, ft *storage.FrameTable) (block.Chunker, error) {
	// Get both sizes from storage backend:
	// - uSize: uncompressed/logical size (U space)
	// - rawSize: actual file size on storage (C space for compressed, same as U for uncompressed)
	uSize, rawSize, err := b.persistence.Size(ctx, b.objectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get object sizes for %s: %w", b.objectPath, err)
	}

	isCompressed := storage.IsCompressed(ft)
	if isCompressed {
		switch storage.CompressedChunkerType {
		case storage.DecompressMMapChunker:
			return block.NewDecompressMMapChunker(uSize, rawSize, b.blockSize, b.persistence, b.objectPath, b.cachePath, b.metrics)
		case storage.CompressLRUChunker:
			return block.NewCompressLRUChunker(uSize, b.persistence, b.objectPath, 4, b.metrics)
		case storage.CompressMMapLRUChunker:
			return block.NewCompressMMapLRUChunker(uSize, rawSize, b.persistence, b.objectPath, b.cachePath, 4, b.metrics)
		default:
			return nil, fmt.Errorf("unsupported chunker type for object %s", b.objectPath)
		}
	}

	return block.NewUncompressedMMapChunker(uSize, b.blockSize, b.persistence, b.objectPath, b.cachePath, b.metrics)
}

func (b *StorageDiff) Close() error {
	if b.chunker == nil {
		return nil
	}

	return b.chunker.Close()
}

func (b *StorageDiff) ReadAt(ctx context.Context, p []byte, off int64, ft *storage.FrameTable) (int, error) {
	chunker, err := b.getChunker(ctx, ft)
	if err != nil {
		return 0, err
	}

	slice, err := chunker.Slice(ctx, off, int64(len(p)), ft)
	if err != nil {
		return 0, err
	}

	return copy(p, slice), nil
}

// Slice reads data at offset (in U space) with given length.
func (b *StorageDiff) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	chunker, err := b.getChunker(ctx, ft)
	if err != nil {
		return nil, err
	}

	return chunker.Slice(ctx, off, length, ft)
}

func (b *StorageDiff) CachePath() (string, error) {
	return b.cachePath, nil
}

func (b *StorageDiff) FileSize() (int64, error) {
	if b.chunker == nil {
		return 0, fmt.Errorf("chunker not initialized - call ReadAt or Slice first")
	}

	return b.chunker.FileSize()
}

func (b *StorageDiff) BlockSize() int64 {
	return b.blockSize
}

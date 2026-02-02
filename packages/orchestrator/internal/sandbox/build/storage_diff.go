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
// This is called on first ReadAt/Slice; subsequent calls return the cached chunker.
func (b *StorageDiff) getChunker(ctx context.Context, ft *storage.FrameTable) (block.Chunker, error) {
	b.chunkerOnce.Do(func() {
		b.chunker, b.chunkerErr = b.createChunker(ctx, ft)
	})

	return b.chunker, b.chunkerErr
}

// createChunker creates the appropriate chunker based on the frame table.
func (b *StorageDiff) createChunker(ctx context.Context, ft *storage.FrameTable) (block.Chunker, error) {
	// Determine if data is compressed and calculate size
	var size int64

	if ft != nil && ft.IsCompressed() {
		// For compressed data, calculate uncompressed size from frame table
		size = ft.StartAt.U + ft.TotalUncompressedSize()
		// Use decompress chunker for compressed data
		return block.NewDecompressMMapChunker(size, b.blockSize, b.persistence, b.objectPath, ft, b.cachePath, b.metrics)
	}

	// For uncompressed data, get the size from storage
	var err error
	size, err = b.persistence.Size(ctx, b.objectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get object size for %s: %w", b.objectPath, err)
	}

	return block.NewUncompressedMMapChunker(size, b.blockSize, b.persistence, b.objectPath, ft, b.cachePath, b.metrics)
}

func (b *StorageDiff) Close() error {
	// If chunker was never initialized, nothing to close
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

	return chunker.ReadAt(ctx, p, off)
}

func (b *StorageDiff) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	chunker, err := b.getChunker(ctx, ft)
	if err != nil {
		return nil, err
	}

	return chunker.Slice(ctx, off, length)
}

// CachePath returns the local cache path for this diff.
func (b *StorageDiff) CachePath() (string, error) {
	return b.cachePath, nil
}

func (b *StorageDiff) FileSize() (int64, error) {
	// FileSize requires chunker to be initialized
	if b.chunker == nil {
		return 0, fmt.Errorf("chunker not initialized - call ReadAt or Slice first")
	}

	return b.chunker.FileSize()
}

func (b *StorageDiff) BlockSize() int64 {
	return b.blockSize
}

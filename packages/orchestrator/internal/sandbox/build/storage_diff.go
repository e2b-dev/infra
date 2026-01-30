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

// ChunkerType specifies which chunker implementation to use.
type ChunkerType int

const (
	// ChunkerTypeMmap uses the traditional mmap-based chunker that stores uncompressed data.
	ChunkerTypeMmap ChunkerType = iota
	// ChunkerTypeCompressed uses an LRU cache for decompressed chunks and an append-only
	// file cache for compressed frames, reducing disk I/O for compressed data.
	ChunkerTypeCompressed
)

type StorageDiff struct {
	dataSource  *utils.SetOnce[block.DataSource]
	cachePath   string
	cacheKey    DiffStoreKey
	chunkerType ChunkerType
	lruSize     int // Number of 4MB chunks in LRU cache (only for ChunkerTypeCompressed)

	blockSize int64
	metrics   blockmetrics.Metrics

	objectPath  string
	persistence storage.StorageProvider
	frameTable  *storage.FrameTable
}

var _ Diff = (*StorageDiff)(nil)

type UnknownDiffTypeError struct {
	DiffType DiffType
}

func (e UnknownDiffTypeError) Error() string {
	return fmt.Sprintf("unknown diff type: %s", e.DiffType)
}

// StorageDiffOption is a functional option for configuring StorageDiff.
type StorageDiffOption func(*StorageDiff)

// WithChunkerType sets the chunker implementation to use.
func WithChunkerType(t ChunkerType) StorageDiffOption {
	return func(s *StorageDiff) {
		s.chunkerType = t
	}
}

// WithLRUSize sets the LRU cache size for compressed chunker (number of 4MB chunks).
func WithLRUSize(size int) StorageDiffOption {
	return func(s *StorageDiff) {
		s.lruSize = size
	}
}

func newStorageDiff(
	basePath string,
	buildId string,
	diffType DiffType,
	blockSize int64,
	metrics blockmetrics.Metrics,
	persistence storage.StorageProvider,
	frameTable *storage.FrameTable,
	opts ...StorageDiffOption,
) (*StorageDiff, error) {
	storagePath := storagePath(buildId, diffType)
	_, ok := storageObjectType(diffType)
	if !ok {
		return nil, UnknownDiffTypeError{diffType}
	}

	cachePath := GenerateDiffCachePath(basePath, buildId, diffType)

	// For compressed data, include the frame table's offset range in the cache key.
	// This is necessary because:
	// 1. Different mappings to the same build have different frame table subsets
	// 2. Each subset covers a different offset range
	// 3. The CompressedChunker needs the correct frame table to serve requests
	// Without this, all mappings would share one CompressedChunker with the wrong frame table.
	// We include BOTH StartAt and TotalUncompressedSize because two subsets can start at the
	// same frame boundary but cover different ranges (e.g., 1 frame vs 2 frames).
	cacheKey := GetDiffStoreKey(buildId, diffType)
	if frameTable != nil && frameTable.IsCompressed() {
		// Include start offset AND total size to differentiate cache entries
		ftEnd := frameTable.StartAt.U + frameTable.TotalUncompressedSize()
		cacheKey = DiffStoreKey(fmt.Sprintf("%s/%s@%x-%x", buildId, diffType, frameTable.StartAt.U, ftEnd))
	}

	sd := &StorageDiff{
		objectPath:  storagePath,
		cachePath:   cachePath,
		dataSource:  utils.NewSetOnce[block.DataSource](),
		blockSize:   blockSize,
		metrics:     metrics,
		persistence: persistence,
		frameTable:  frameTable,
		cacheKey:    cacheKey,
		chunkerType: ChunkerTypeMmap, // Default to existing behavior
	}

	for _, opt := range opts {
		opt(sd)
	}

	return sd, nil
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

func (b *StorageDiff) Init(ctx context.Context) error {
	var size int64
	var err error

	if b.frameTable.IsCompressed() {
		// For compressed data, use the frame table to calculate the uncompressed size.
		// This works correctly now because each mapping with a different frame table
		// offset gets its own cached StorageDiff (via the cache key including the offset).
		size = b.frameTable.StartAt.U + b.frameTable.TotalUncompressedSize()
	} else {
		// For uncompressed data, get the size from storage.
		size, err = b.persistence.Size(ctx, b.objectPath)
		if err != nil {
			errMsg := fmt.Errorf("failed to get object size: %w", err)
			b.dataSource.SetError(errMsg)

			return errMsg
		}
	}

	var dataSource block.DataSource

	switch b.chunkerType {
	case ChunkerTypeCompressed:
		dataSource, err = block.NewCompressedChunker(
			size,
			b.persistence,
			b.objectPath,
			b.frameTable,
			b.lruSize,
			b.metrics,
		)
	default: // ChunkerTypeMmap
		dataSource, err = block.NewChunker(size, b.blockSize, b.persistence, b.objectPath, b.frameTable, b.cachePath, b.metrics)
	}

	if err != nil {
		errMsg := fmt.Errorf("failed to create chunker: %w", err)
		b.dataSource.SetError(errMsg)

		return errMsg
	}

	return b.dataSource.SetValue(dataSource)
}

func (b *StorageDiff) Close() error {
	ds, err := b.dataSource.Wait()
	if err != nil {
		return err
	}

	return ds.Close()
}

func (b *StorageDiff) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	ds, err := b.dataSource.Wait()
	if err != nil {
		return 0, err
	}

	return ds.ReadAt(ctx, p, off)
}

func (b *StorageDiff) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	ds, err := b.dataSource.Wait()
	if err != nil {
		return nil, err
	}

	return ds.Slice(ctx, off, length)
}

// The local file might not be synced.
func (b *StorageDiff) CachePath() (string, error) {
	return b.cachePath, nil
}

func (b *StorageDiff) FileSize() (int64, error) {
	ds, err := b.dataSource.Wait()
	if err != nil {
		return 0, err
	}

	return ds.FileSize()
}

func (b *StorageDiff) Size(_ context.Context) (int64, error) {
	return b.FileSize()
}

func (b *StorageDiff) BlockSize() int64 {
	return b.blockSize
}

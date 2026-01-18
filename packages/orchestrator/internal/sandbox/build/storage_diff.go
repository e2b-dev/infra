package build

import (
	"context"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func storagePath(buildId string, diffType DiffType) string {
	return fmt.Sprintf("%s/%s", buildId, diffType)
}

type StorageDiff struct {
	chunker   *utils.SetOnce[*block.Chunker]
	cachePath string
	cacheKey  DiffStoreKey

	blockSize int64
	metrics   blockmetrics.Metrics

	persistencePath string
	persistence     storage.API
	frameTable      *storage.FrameTable
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
	persistence storage.API,
	frameTable *storage.FrameTable,
) (*StorageDiff, error) {
	storagePath := storagePath(buildId, diffType)
	_, ok := storageObjectType(diffType)
	if !ok {
		return nil, UnknownDiffTypeError{diffType}
	}

	cachePath := GenerateDiffCachePath(basePath, buildId, diffType)

	return &StorageDiff{
		persistencePath: storagePath,
		cachePath:       cachePath,
		chunker:         utils.NewSetOnce[*block.Chunker](),
		blockSize:       blockSize,
		metrics:         metrics,
		persistence:     persistence,
		frameTable:      frameTable,
		cacheKey:        GetDiffStoreKey(buildId, diffType),
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

func (b *StorageDiff) Init(ctx context.Context) error {
	// TODO LEV why do we need to get the size? We already have the header, which has the size; 
	// before we were fetching the header in parallel, now maybe skip this?
	size, err := b.persistence.Size(ctx, b.persistencePath)
	if err != nil {
		errMsg := fmt.Errorf("failed to get object size: %w", err)
		b.chunker.SetError(errMsg)

		return errMsg
	}

	chunker, err := block.NewChunker(size, b.blockSize, b.persistence, b.persistencePath, b.cachePath, b.metrics, b.frameTable)
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

func (b *StorageDiff) Size(_ context.Context) (int64, error) {
	return b.FileSize()
}

func (b *StorageDiff) BlockSize() int64 {
	return b.blockSize
}

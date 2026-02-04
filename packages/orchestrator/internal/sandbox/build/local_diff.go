package build

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

type LocalDiffFile struct {
	*os.File

	cachePath string
	cacheKey  DiffStoreKey
}

func NewLocalDiffFile(
	basePath string,
	buildId string,
	diffType DiffType,
) (*LocalDiffFile, error) {
	cachePath := GenerateDiffCachePath(basePath, buildId, diffType)

	f, err := os.OpenFile(cachePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &LocalDiffFile{
		File:      f,
		cachePath: cachePath,
		cacheKey:  GetDiffStoreKey(buildId, diffType),
	}, nil
}

func (f *LocalDiffFile) Close() error {
	err := f.File.Close()
	if err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	err = os.Remove(f.cachePath)
	if err != nil {
		return fmt.Errorf("failed to remove file: %w", err)
	}

	return nil
}

func (f *LocalDiffFile) CloseToDiff(
	blockSize int64,
) (Diff, error) {
	defer f.File.Close()

	err := f.File.Sync()
	if err != nil {
		return nil, fmt.Errorf("failed to sync file: %w", err)
	}

	size, err := f.File.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file size: %w", err)
	}

	if size.Size() == 0 {
		return &NoDiff{}, nil
	}

	return newLocalDiff(
		f.cacheKey,
		f.cachePath,
		size.Size(),
		blockSize,
	)
}

type localDiff struct {
	cacheKey DiffStoreKey
	cache    *block.Cache
}

var _ Diff = (*localDiff)(nil)

func NewLocalDiffFromCache(
	cacheKey DiffStoreKey,
	cache *block.Cache,
) (Diff, error) {
	return &localDiff{
		cache:    cache,
		cacheKey: cacheKey,
	}, nil
}

func newLocalDiff(
	cacheKey DiffStoreKey,
	cachePath string,
	size,
	blockSize int64,
) (Diff, error) {
	cache, err := block.NewCache(size, blockSize, cachePath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return NewLocalDiffFromCache(cacheKey, cache)
}

func (b *localDiff) CachePath() (string, error) {
	return b.cache.Path(), nil
}

func (b *localDiff) Close() error {
	return b.cache.Close()
}

func (b *localDiff) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	return b.cache.ReadAt(p, off)
}

func (b *localDiff) Slice(_ context.Context, off, length int64) ([]byte, error) {
	return b.cache.Slice(off, length)
}

func (b *localDiff) Size(_ context.Context) (int64, error) {
	return b.cache.Size()
}

func (b *localDiff) FileSize() (int64, error) {
	return b.cache.FileSize()
}

func (b *localDiff) CacheKey() DiffStoreKey {
	return b.cacheKey
}

func (b *localDiff) Init(context.Context) error {
	return nil
}

func (b *localDiff) BlockSize() int64 {
	return b.cache.BlockSize()
}

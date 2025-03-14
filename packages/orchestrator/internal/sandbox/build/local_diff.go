package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
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
	cachePathSuffix := id.Generate()

	cacheFile := fmt.Sprintf("%s-%s-%s", buildId, diffType, cachePathSuffix)
	cachePath := filepath.Join(basePath, cacheFile)

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

func (f *LocalDiffFile) ToDiff(
	blockSize int64,
) (Diff, error) {
	defer f.Close()

	err := f.Sync()
	if err != nil {
		return nil, fmt.Errorf("failed to sync file: %w", err)
	}

	size, err := f.Stat()
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
	size      int64
	blockSize int64
	cacheKey  DiffStoreKey
	cachePath string
	cache     *block.Cache
}

func newLocalDiff(
	cacheKey DiffStoreKey,
	cachePath string,
	size int64,
	blockSize int64,
) (*localDiff, error) {
	cache, err := block.NewCache(size, blockSize, cachePath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return &localDiff{
		size:      size,
		blockSize: blockSize,
		cacheKey:  cacheKey,
		cachePath: cachePath,
		cache:     cache,
	}, nil
}

func (b *localDiff) CachePath() (string, error) {
	return b.cachePath, nil
}

func (b *localDiff) Close() error {
	return b.cache.Close()
}

func (b *localDiff) ReadAt(p []byte, off int64) (int, error) {
	return b.cache.ReadAt(p, off)
}

func (b *localDiff) Slice(off, length int64) ([]byte, error) {
	return b.cache.Slice(off, length)
}

func (b *localDiff) FileSize() (int64, error) {
	return b.cache.FileSize()
}

func (b *localDiff) CacheKey() DiffStoreKey {
	return b.cacheKey
}

func (b *localDiff) Init(ctx context.Context) error {
	return nil
}

package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
)

type LocalDiffFile struct {
	*os.File
	cachePath string
}

func NewLocalDiffFile(
	buildId string,
	diffType DiffType,
) (*LocalDiffFile, error) {
	cachePathSuffix := id.Generate()

	cacheFile := fmt.Sprintf("%s-%s-%s", buildId, diffType, cachePathSuffix)
	cachePath := filepath.Join(cachePath, cacheFile)

	f, err := os.OpenFile(cachePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &LocalDiffFile{
		File:      f,
		cachePath: cachePath,
	}, nil
}

func (f *LocalDiffFile) ToLocalDiff(
	blockSize int64,
) (*LocalDiff, error) {
	err := f.Sync()
	if err != nil {
		return nil, fmt.Errorf("failed to sync file: %w", err)
	}

	size, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file size: %w", err)
	}

	return newLocalDiff(f.cachePath, size.Size(), blockSize)
}

type LocalDiff struct {
	size      int64
	blockSize int64
	cachePath string
	cache     *block.Cache
}

func newLocalDiff(
	cachePath string,
	size int64,
	blockSize int64,
) (*LocalDiff, error) {
	cache, err := block.NewCache(size, blockSize, cachePath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return &LocalDiff{
		size:      size,
		blockSize: blockSize,
		cachePath: cachePath,
		cache:     cache,
	}, nil
}

func (b *LocalDiff) Path() (string, error) {
	err := b.cache.Sync()
	if err != nil {
		return "", fmt.Errorf("failed to sync cache: %w", err)
	}

	return b.cachePath, nil
}

func (b *LocalDiff) Close() error {
	return b.cache.Close()
}

func (b *LocalDiff) ReadAt(p []byte, off int64) (int, error) {
	return b.cache.ReadAt(p, off)
}

func (b *LocalDiff) Size() (int64, error) {
	return b.size, nil
}

func (b *LocalDiff) Slice(off, length int64) ([]byte, error) {
	return b.cache.Slice(off, length)
}

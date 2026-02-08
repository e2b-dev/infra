package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type DiffType string

type NoDiffError struct{}

func (NoDiffError) Error() string {
	return "the diff is empty"
}

const (
	Memfile DiffType = storage.MemfileName
	Rootfs  DiffType = storage.RootfsName
)

// Diff represents a build's file (memfile or rootfs) that can be read.
// Implementations must support lazy initialization using the frame table
// provided to ReadAt/Slice on first access.
type Diff interface {
	io.Closer
	CacheKey() DiffStoreKey
	CachePath() (string, error)
	FileSize() (int64, error)
	BlockSize() int64
	// ReadAt reads data at offset. The frame table is used for lazy chunker init.
	ReadAt(ctx context.Context, p []byte, off int64, ft *storage.FrameTable) (int, error)
	// Slice returns a view into the data. The frame table is used for lazy chunker init.
	Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error)
}

type NoDiff struct{}

var _ Diff = (*NoDiff)(nil)

func (n *NoDiff) CachePath() (string, error) {
	return "", NoDiffError{}
}

func (n *NoDiff) Slice(_ context.Context, _, _ int64, _ *storage.FrameTable) ([]byte, error) {
	return nil, NoDiffError{}
}

func (n *NoDiff) Close() error {
	return nil
}

func (n *NoDiff) ReadAt(_ context.Context, _ []byte, _ int64, _ *storage.FrameTable) (int, error) {
	return 0, NoDiffError{}
}

func (n *NoDiff) FileSize() (int64, error) {
	return 0, NoDiffError{}
}

func (n *NoDiff) Size(_ context.Context) (int64, error) {
	return 0, NoDiffError{}
}

func (n *NoDiff) CacheKey() DiffStoreKey {
	return ""
}

func (n *NoDiff) BlockSize() int64 {
	return 0
}

func GenerateDiffCachePath(basePath string, buildId string, diffType DiffType) string {
	cachePathSuffix := id.Generate()

	cacheFile := fmt.Sprintf("%s-%s-%s", buildId, diffType, cachePathSuffix)

	return filepath.Join(basePath, cacheFile)
}

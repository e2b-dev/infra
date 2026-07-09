//go:build linux

package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
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

type Diff interface {
	io.Closer
	block.FramedReader
	block.FramedSlicer
	CacheKey() DiffStoreKey
	CachePath(ctx context.Context) (string, error)
	// Size returns the logical (uncompressed, U-space) file size.
	Size(ctx context.Context) (int64, error)
	// FileSize returns the number of bytes resident in the local cache file
	// on disk. Used by the DiffStore evictor.
	FileSize(ctx context.Context) (int64, error)
	BlockSize() int64
}

type NoDiff struct{}

var _ Diff = (*NoDiff)(nil)

func (n *NoDiff) CachePath(context.Context) (string, error) {
	return "", nil
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

func (n *NoDiff) FileSize(_ context.Context) (int64, error) {
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

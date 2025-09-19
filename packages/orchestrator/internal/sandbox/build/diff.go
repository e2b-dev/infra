package build

import (
	"context"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
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
	storage.ReaderAtCtx
	block.Slicer
	CacheKey() DiffStoreKey
	CachePath() (string, error)
	FileSize() (int64, error)
	Init(ctx context.Context) error
}

type NoDiff struct{}

var _ Diff = (*NoDiff)(nil)

func (n *NoDiff) CachePath() (string, error) {
	return "", NoDiffError{}
}

func (n *NoDiff) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	return nil, NoDiffError{}
}

func (n *NoDiff) Close() error {
	return nil
}

func (n *NoDiff) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return 0, NoDiffError{}
}

func (n *NoDiff) FileSize() (int64, error) {
	return 0, NoDiffError{}
}

func (n *NoDiff) CacheKey() DiffStoreKey {
	return ""
}

func (n *NoDiff) Init(ctx context.Context) error {
	return NoDiffError{}
}

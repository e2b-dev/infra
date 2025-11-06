package build

import (
	"context"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/paths"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type DiffType string

type NoDiffError struct{}

func (NoDiffError) Error() string {
	return "the diff is empty"
}

const (
	Memfile DiffType = paths.MemfileName
	Rootfs  DiffType = paths.RootfsName
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

func (n *NoDiff) Slice(_ context.Context, _, _ int64) ([]byte, error) {
	return nil, NoDiffError{}
}

func (n *NoDiff) Close() error {
	return nil
}

func (n *NoDiff) ReadAt(_ context.Context, _ []byte, _ int64) (int, error) {
	return 0, NoDiffError{}
}

func (n *NoDiff) FileSize() (int64, error) {
	return 0, NoDiffError{}
}

func (n *NoDiff) CacheKey() DiffStoreKey {
	return ""
}

func (n *NoDiff) Init(context.Context) error {
	return NoDiffError{}
}

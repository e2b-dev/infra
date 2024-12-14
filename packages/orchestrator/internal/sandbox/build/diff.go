package build

import (
	"errors"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type DiffType string

var ErrNoDiff = errors.New("no diff")

const (
	Memfile DiffType = storage.MemfileName
	Rootfs  DiffType = storage.RootfsName
)

type Diff interface {
	io.Closer
	io.ReaderAt
	Size() (int64, error)
	Slice(off, length int64) ([]byte, error)
	Path() (string, error)
}

type NoDiff struct{}

func (n *NoDiff) Path() (string, error) {
	return "", ErrNoDiff
}

func (n *NoDiff) Size() (int64, error) {
	return 0, nil
}

func (n *NoDiff) Slice(off, length int64) ([]byte, error) {
	return nil, ErrNoDiff
}

func (n *NoDiff) Close() error {
	return nil
}

func (n *NoDiff) ReadAt(p []byte, off int64) (int, error) {
	return 0, ErrNoDiff
}

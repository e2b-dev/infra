package build

import (
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type DiffType string

const (
	Memfile DiffType = storage.MemfileName
	Rootfs  DiffType = storage.RootfsName
)

type Diff interface {
	io.Closer
	io.ReaderAt
	Size() (int64, error)
	Slice(off, length int64) ([]byte, error)
}

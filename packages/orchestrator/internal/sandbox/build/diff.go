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
	Init() error
	io.Closer
	io.ReaderAt
	io.WriterTo
	Size() (int64, error)
	Slice(off, length int64) ([]byte, error)
}

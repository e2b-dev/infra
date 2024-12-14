package build

import "io"

type Diff interface {
	Init() error
	io.Closer
	io.ReaderAt
	io.WriterTo
	Size() (int64, error)
	Slice(off, length int64) ([]byte, error)
}

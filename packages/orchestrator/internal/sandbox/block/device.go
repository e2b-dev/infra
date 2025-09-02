package block

import (
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type BytesNotAvailableError struct{}

func (BytesNotAvailableError) Error() string {
	return "The requested bytes are not available on the device"
}

type Slicer interface {
	Slice(off, length int64) ([]byte, error)
}

type ReadonlyDevice interface {
	io.ReaderAt
	io.Closer
	Slicer
	Size() (int64, error)
	BlockSize() int64
	Header() *header.Header
}

type Device interface {
	ReadonlyDevice
	io.WriterAt
}

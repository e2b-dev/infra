package block

import (
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type ErrBytesNotAvailable struct{}

func (ErrBytesNotAvailable) Error() string {
	return "The requested bytes are not available on the device"
}

type ReadonlyDevice interface {
	io.ReaderAt
	Slice(off, length int64) ([]byte, error)
	Size() (int64, error)
	BlockSize() int64
	Header() *header.Header
}

type Device interface {
	ReadonlyDevice
	io.WriterAt
	Close() error
}

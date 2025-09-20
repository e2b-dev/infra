package block

import (
	"context"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type BytesNotAvailableError struct{}

func (BytesNotAvailableError) Error() string {
	return "The requested bytes are not available on the device"
}

type Slicer interface {
	Slice(ctx context.Context, off, length int64) ([]byte, error)
}

type ReadonlyDevice interface {
	storage.ReaderAtCtx
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

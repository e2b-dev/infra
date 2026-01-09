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
	BlockSize() int64
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

// ChunksFetchedProvider is an optional interface for devices that track remote chunk fetches.
type ChunksFetchedProvider interface {
	ChunksFetched() int64
}

package block

import (
	"context"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// BytesNotAvailableError indicates the requested range is not yet cached.
type BytesNotAvailableError struct{}

func (BytesNotAvailableError) Error() string {
	return "The requested bytes are not available on the device"
}

// Reader reads data with optional FrameTable for compressed fetch.
type Reader interface {
	ReadBlock(ctx context.Context, p []byte, off int64, ft *storage.FrameTable) (int, error)
	GetBlock(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error)
}

// Slicer provides plain block reads (no FrameTable). Used by UFFD/NBD.
type Slicer interface {
	Slice(ctx context.Context, off, length int64) ([]byte, error)
	BlockSize() int64
}

type ReadonlyDevice interface {
	ReadAt(ctx context.Context, p []byte, off int64) (int, error)
	Size(ctx context.Context) (int64, error)
	io.Closer
	Slicer
	BlockSize() int64
	Header() *header.Header
}

type Device interface {
	ReadonlyDevice
	io.WriterAt
}

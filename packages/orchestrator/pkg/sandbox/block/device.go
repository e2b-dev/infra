//go:build linux

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

type FramedReader interface {
	ReadAt(ctx context.Context, p []byte, off int64, ft *storage.FrameTable) (int, error)
}

type FramedSlicer interface {
	Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error)
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
	SwapHeader(h *header.Header)
}

// DiffSource is the set of methods build.localDiff needs from its backing
// store. Both *Cache (sync) and *MemfdCache (async, in-flight reads) satisfy
// it, so the diff layer doesn't need to know which one it has.
type DiffSource interface {
	io.Closer
	io.ReaderAt
	Slice(off, length int64) ([]byte, error)
	Size() (int64, error)
	FileSize() (int64, error)
	BlockSize() int64
	Path() string
}

type Device interface {
	ReadonlyDevice
	io.WriterAt
	WriteZeroesAt(off, length int64) (int, error)
}

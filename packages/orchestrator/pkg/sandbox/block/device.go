//go:build linux

package block

import (
	"context"
	"io"
	"time"

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

type Device interface {
	ReadonlyDevice
	io.WriterAt
	WriteZeroesAt(off, length int64) (int, error)
}

// CachePeeker reports whether [off, off+length) is in the local cache,
// without triggering a remote fetch.
type CachePeeker interface {
	IsCached(ctx context.Context, off, length int64) bool
}

// FetchWaiter answers when the fetches overlapping a range have terminated,
// and how long one may take: a chunk is marked present only when its whole
// fetch completes, so a presence query taken while one is in flight reads
// the chunk as absent.
type FetchWaiter interface {
	WaitFetched(ctx context.Context, off, length int64) error
	FetchTimeout() time.Duration
}

// DiffSource is what the diff/upload layer reads from. *Cache satisfies it
// directly; *MemfdCache wraps *Cache, waits for the background copy to complete
// and then delegates to *Cache.
type DiffSource interface {
	io.Closer
	ReadAt(b []byte, off int64) (int, error)
	Slice(off, length int64) ([]byte, error)
	Size() (int64, error)
	FileSize(ctx context.Context) (int64, error)
	BlockSize() int64
	Path(ctx context.Context) (string, error)
}

package block

import (
	"io"
)

type ErrBytesNotAvailable struct{}

func (ErrBytesNotAvailable) Error() string {
	return "The requested bytes are not available on the device"
}

// The offset is in bytes and should be aligned to the block size.
type Device interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
	Size() (int64, error)
	// ReadRaw exposed the underlying byte slice from the device.
	// The caller must call the close function when the byte slice is no longer needed
	// so it can be released back to the cache.
	ReadRaw(off, length int64) ([]byte, func(), error)
	BlockSize() int64
	// IsMarked returns true if the offset is marked as dirty in the cache
	IsMarked(offset int64) bool
}

package block

import (
	"io"
)

type ErrBytesNotAvailable struct{}

func (ErrBytesNotAvailable) Error() string {
	return "The requested bytes are not available on the device"
}

// The block size is defined by the Size constant.
// The offset is in bytes and should be aligned to the block size.
type Device interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
	Size() (int64, error)
	ReadRaw(off, length int64) ([]byte, func(), error)
	BlockSize() int64
}

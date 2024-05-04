package block

import (
	"io"
)

// Size is the size of a block in bytes.
// This needs to be accurate to the filesystem block size we are using.
// The reads and writes from the block device should be between 512 and 4096 bytes.
const Size int64 = 4096 // 4KB

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
}

package block

import (
	"io"
)

const (
	Size int64 = 4096 // 4KB

	// What is the optimal superblock size for this?
)

type ErrBytesNotAvailable struct{}

func (ErrBytesNotAvailable) Error() string {
	return "The requested bytes are not available in the device"
}

// The block size is defined by the BlockSize constant.
// The offset is in bytes and should be aligned to the block size.
type Device interface {
	io.ReaderAt
	io.WriterAt
}

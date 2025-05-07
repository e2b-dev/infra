package block

import "io"

type ErrBytesNotAvailable struct{}

func (ErrBytesNotAvailable) Error() string {
	return "The requested bytes are not available on the device"
}

type ReadonlyDevice interface {
	io.ReaderAt
	Slice(off, length int64) ([]byte, error)
	Size() (int64, error)
	BlockSize() int64
}

type Device interface {
	ReadonlyDevice
	io.WriterAt
	Close() error
}

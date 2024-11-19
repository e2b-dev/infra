package block

import "io"

type ErrBytesNotAvailable struct{}

func (ErrBytesNotAvailable) Error() string {
	return "The requested bytes are not available on the device"
}

type ReadonlyDevice interface {
	io.ReaderAt
	Size() (int64, error)
	Close() error
	BlockSize() int64
	Slice(off, length int64) ([]byte, error)
}

type Device interface {
	ReadonlyDevice
	io.WriterAt
	Sync() error
}

package storage

import (
	"io"
	"os"
)

type offsetReader struct {
	wrapped io.ReaderAt
	offset  int64
}

var _ io.Reader = (*offsetReader)(nil)

func (r *offsetReader) Read(p []byte) (n int, err error) {
	n, err = r.wrapped.ReadAt(p, r.offset)
	r.offset += int64(n)

	return
}

func newOffsetReader(file *os.File, offset int64) *offsetReader {
	return &offsetReader{file, offset}
}

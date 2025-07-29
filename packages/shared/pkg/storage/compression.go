package storage

import (
	"fmt"
	"github.com/pierrec/lz4/v4"
	"io"
)

/*
- WriteTo = decompress
- WriteFromFileSystem = compress
- ReadFrom = compress
- ReadAt = decompress (probably complicated!)
*/

func WithCompression(provider StorageObjectProvider) StorageObjectProvider {
	return &compressor{provider}
}

type compressor struct {
	wrapped StorageObjectProvider
}

var _ StorageObjectProvider = (*compressor)(nil)

func (c compressor) WriteTo(dst io.Writer) (int64, error) {
	dst = newDecompressingWriter(dst)
	return c.wrapped.WriteTo(dst)
}

func (c compressor) WriteFrom(reader io.ReadCloser, length int64) error {
	compressingReader := lz4.NewCompressingReader(reader)

	if err := c.wrapped.WriteFrom(compressingReader, length); err != nil {
		return fmt.Errorf("failed to compress file: %w", err)
	}

	return nil
}

func (c compressor) ReadFrom(src io.Reader) (int64, error) {
	src = lz4.NewReader(src)
	return c.wrapped.ReadFrom(src)
}

func (c compressor) ReadAt(buff []byte, off int64) (n int, err error) {
	return c.wrapped.ReadAt(buff, off)
}

func (c compressor) Size() (int64, error) {
	return c.wrapped.Size()
}

func (c compressor) Delete() error {
	return c.wrapped.Delete()
}

type decompressingWriter struct {
	wrapped io.Writer
}

func (d decompressingWriter) Write(src []byte) (n int, err error) {
	var dst []byte
	count, err := lz4.UncompressBlock(src, dst)
	return d.wrapped.Write(dst[:count])
}

func newDecompressingWriter(writer io.Writer) io.Writer {
	return decompressingWriter{writer}
}

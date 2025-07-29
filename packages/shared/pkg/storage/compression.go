package storage

import (
	"fmt"
	"github.com/pierrec/lz4/v4"
	"io"
	"os"
)

func WithCompression(provider StorageObjectProvider) StorageObjectProvider {
	return &compressor{provider}
}

type compressor struct {
	wrapped StorageObjectProvider
}

func (c compressor) WriteTo(dst io.Writer) (int64, error) {
	dst = lz4.NewWriter(dst)
	return c.wrapped.WriteTo(dst)
}

func (c compressor) WriteFromFileSystem(path string) error {
	reader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	tempFile, err := os.CreateTemp("", "compression.*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	writer := lz4.NewWriter(tempFile)
	if _, err = io.Copy(writer, reader); err != nil {
		return fmt.Errorf("failed to compress file")
	}
	reader.Close()
	tempFile.Close()

	return c.wrapped.WriteFromFileSystem(tempFile.Name())
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

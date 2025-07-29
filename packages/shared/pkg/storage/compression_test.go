package storage

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"testing"
)

type simpleFile struct {
	path string
}

func (s simpleFile) WriteTo(dst io.Writer) (int64, error) {
	fp, err := os.Open(s.path)
	if err != nil {
		return 0, fmt.Errorf("failed to open file %s: %w", s.path, err)
	}
	defer fp.Close()

	return io.Copy(dst, fp)
}

func (s simpleFile) WriteFromFileSystem(path string) error {
	input, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", s.path, err)
	}
	defer input.Close()

	output, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", s.path, err)
	}

	if _, err = io.Copy(output, input); err != nil {
		return fmt.Errorf("failed to copy %s: %w", s.path, err)
	}

	return nil
}

func (s simpleFile) ReadFrom(src io.Reader) (int64, error) {
	//TODO implement me
	panic("implement me")
}

func (s simpleFile) ReadAt(buff []byte, off int64) (n int, err error) {
	//TODO implement me
	panic("implement me")
}

func (s simpleFile) Size() (int64, error) {
	//TODO implement me
	panic("implement me")
}

func (s simpleFile) Delete() error {
	//TODO implement me
	panic("implement me")
}

var _ StorageObjectProvider = (*simpleFile)(nil)

func TestCompression(t *testing.T) {
	t.Run("WriteFromFileSystem, then WriteTo", func(t *testing.T) {
		mock := NewMockStorageObjectProvider(t)
		c := WithCompression(mock)

		tempFile, err := os.CreateTemp("", "")
		require.NoError(t, err)
		t.Cleanup(func() { tempFile.Close() })

		_, err = io.WriteString(tempFile, "hello world")
		require.NoError(t, err)

		err = c.WriteFromFileSystem(tempFile.Name())
		require.NoError(t, err)

	})

	t.Run("ReadFrom, then ReadAt", func(t *testing.T) {
		mock := NewMockStorageObjectProvider(t)
		c := WithCompression(mock)

		var buff []byte
		count, err := c.ReadAt(buff, 5)
		require.NoError(t, err)
		assert.Equal(t, int64(5), count)
	})
}

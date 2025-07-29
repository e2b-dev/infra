package storage

import (
	"bytes"
	"fmt"
	"github.com/google/uuid"
	"github.com/pierrec/lz4/v4"
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

func (s simpleFile) WriteFrom(input io.ReadCloser, length int64) error {
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
	input, err := os.OpenFile(s.path, os.O_WRONLY, 0600)
	if err != nil {
		return 0, fmt.Errorf("failed to open file")
	}
	defer input.Close()

	return input.ReadFrom(src)
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

func createTempFile(t *testing.T, pattern string) string {
	t.Helper()

	return createTempFileWithContent(t, pattern, "")
}

func createTempFileWithContent(t *testing.T, pattern, content string) string {
	t.Helper()

	tf, err := os.CreateTemp("", pattern)
	require.NoError(t, err)

	if content != "" {
		_, err = io.WriteString(tf, content)
		require.NoError(t, err)
	}

	err = tf.Close()
	require.NoError(t, err)

	t.Cleanup(func() {
		err = os.Remove(tf.Name())
		require.NoError(t, err)
	})

	return tf.Name()
}

func TestCompression(t *testing.T) {
	t.Run("WriteFromFileSystem, then WriteTo", func(t *testing.T) {
		// create a plain text file
		plainText := uuid.NewString()
		plainTextPath := createTempFileWithContent(t, "*.txt", plainText)
		compressedPath := createTempFile(t, "*.lz4")

		// create object source, wrap in compressor
		file := simpleFile{path: compressedPath}
		c := WithCompression(file)

		// compress the data to
		fp, err := os.Open(plainTextPath)
		require.NoError(t, err)
		err = c.WriteFrom(fp, int64(len(plainText)))
		require.NoError(t, err)

		// verify data is compressed
		fp, err = os.Open(compressedPath)
		require.NoError(t, err)
		r := lz4.NewReader(fp)
		result, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Equal(t, plainText, string(result))

		var buf bytes.Buffer
		_, err = c.WriteTo(&buf)
		require.NoError(t, err)

		assert.Equal(t, plainText, buf.String())
	})

	t.Run("ReadFrom, then WriteTo", func(t *testing.T) {
		//mock := NewMockStorageObjectProvider(t)
		//c := WithCompression(mock)
		//
		//var buff []byte
		//count, err := c.ReadAt(buff, 5)
		//require.NoError(t, err)
		//assert.Equal(t, int64(5), count)
	})
}

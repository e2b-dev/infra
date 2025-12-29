package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	storagemocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks"
)

func TestCachedObjectProvider_WriteFromFileSystem(t *testing.T) {
	t.Run("can be cached successfully", func(t *testing.T) {
		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		tempFilename := filepath.Join(tempDir, "temp.bin")
		data := []byte("hello world")

		err := os.MkdirAll(cacheDir, os.ModePerm)
		require.NoError(t, err)

		err = os.WriteFile(tempFilename, data, 0o644)
		require.NoError(t, err)

		inner := storagemocks.NewMockObjectProvider(t)
		inner.EXPECT().
			WriteFromFileSystem(mock.Anything, mock.Anything).
			Return(nil)

		c := CachedObjectProvider{path: cacheDir, inner: inner, chunkSize: 1024}

		// write temp file
		err = c.WriteFromFileSystem(t.Context(), tempFilename)
		require.NoError(t, err)

		// file is written asynchronously, wait for it to finish
		time.Sleep(20 * time.Millisecond)

		// prevent the provider from falling back to cache
		c.inner = nil

		var buff bytes.Buffer
		bytesRead, err := c.WriteTo(t.Context(), &buff)
		require.NoError(t, err)
		assert.Equal(t, data, buff.Bytes())
		assert.Equal(t, int64(len(data)), bytesRead)
	})

	t.Run("uncached reads will be cached the second time", func(t *testing.T) {
		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		err := os.MkdirAll(cacheDir, 0o777)
		require.NoError(t, err)

		const dataSize = 10 * megabyte
		actualData := generateData(dataSize)

		inner := storagemocks.NewMockObjectProvider(t)
		inner.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
				num, err := w.Write(actualData)

				return int64(num), err
			})

		c := CachedObjectProvider{path: cacheDir, inner: inner, chunkSize: 1024}

		buff := bytes.NewBuffer(nil)
		bytesRead, err := c.WriteTo(t.Context(), buff)
		require.NoError(t, err)
		assert.Equal(t, int64(dataSize), bytesRead)
		assert.Equal(t, actualData, buff.Bytes())

		time.Sleep(20 * time.Millisecond)

		c.inner = nil

		buff = bytes.NewBuffer(nil)
		bytesRead, err = c.WriteTo(t.Context(), buff)
		require.NoError(t, err)
		assert.Equal(t, int64(dataSize), bytesRead)
		assert.Equal(t, actualData, buff.Bytes())
	})
}

func TestCachedObjectProvider_WriteFileToCache(t *testing.T) {
	c := CachedObjectProvider{
		path: t.TempDir(),
	}
	errTarget := errors.New("find me")
	reader := storagemocks.NewMockReader(t)
	reader.EXPECT().Read(mock.Anything).Return(4, nil).Once()
	reader.EXPECT().Read(mock.Anything).Return(0, errTarget).Once()

	count, err := c.writeFileToCache(t.Context(), reader)
	require.ErrorIs(t, err, errTarget)
	assert.Equal(t, int64(0), count)

	path := c.fullFilename()
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func generateData(count int) []byte {
	data := make([]byte, count)
	for i := range count {
		data[i] = byte(rand.Intn(256))
	}

	return data
}

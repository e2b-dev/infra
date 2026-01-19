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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

var noopTracer = noop.TracerProvider{}.Tracer("")

func TestCachedStorage_UploadDownload(t *testing.T) {
	t.Parallel()

	t.Run("can be cached successfully", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		data := []byte("hello world")

		err := os.MkdirAll(cacheDir, os.ModePerm)
		require.NoError(t, err)

		inner := NewMockAPI(t)
		inner.EXPECT().
			Upload(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, objectPath string, src io.Reader, size int64) (int64, error) {
				return size, nil
			})
		featureFlags := NewMockFeatureFlagsClient(t)
		featureFlags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(true)

		c := &Cache{
			rootPath:  cacheDir,
			inner:     inner,
			chunkSize: 1024,
			flags:     featureFlags,
			tracer:    noopTracer,
		}

		// write temp file
		n, wg, err := c.upload(t.Context(), "test-item", data)
		require.NoError(t, err)
		require.NotNil(t, wg)
		require.Equal(t, int64(len(data)), n)

		// file is written asynchronously, wait for it to finish
		wg.Wait()

		// prevent the provider from falling back to cache
		c.inner = nil

		gotData, err := GetBlob(t.Context(), c, "test-item", nil)
		require.NoError(t, err)
		assert.Equal(t, data, gotData)
	})

	t.Run("uncached reads will be cached the second time", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		err := os.MkdirAll(cacheDir, 0o777)
		require.NoError(t, err)

		const dataSize = 10 * megabyte
		actualData := generateData(t, dataSize)

		inner := NewMockAPI(t)
		inner.EXPECT().
			Download(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, objectPath string, dst io.Writer) (int64, error) {
				n, err := dst.Write(actualData)

				return int64(n), err
			})

		c := &Cache{
			rootPath:  cacheDir,
			inner:     inner,
			chunkSize: 1024,
			tracer:    noopTracer,
		}

		buf := bytes.NewBuffer(nil)
		read, wg, err := c.download(t.Context(), "test-item", buf)
		require.NoError(t, err)
		assert.Equal(t, int64(len(actualData)), read)
		assert.Equal(t, actualData, buf.Bytes())

		wg.Wait()

		c.inner = nil

		buf = bytes.NewBuffer(nil)
		read, wg, err = c.download(t.Context(), "test-item", buf)
		require.NoError(t, err)
		assert.Equal(t, int64(len(actualData)), read)
		assert.Equal(t, actualData, buf.Bytes())
	})
}

func TestCachedStorage_StoreFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	c := Cache{
		rootPath: root,
		tracer:   noopTracer,
	}
	errTarget := errors.New("find me")
	reader := NewMockReader(t)
	reader.EXPECT().Read(mock.Anything).Return(4, nil).Once()
	reader.EXPECT().Read(mock.Anything).Return(0, errTarget).Once()

	count, err := c.writeFileToCache(t.Context(), "test-item", reader)
	require.ErrorIs(t, err, errTarget)
	assert.Equal(t, int64(0), count)

	path := fullFilename(filepath.Join(root, "test-item"))
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func generateData(t *testing.T, count int) []byte {
	t.Helper()

	data := make([]byte, count)
	for i := range count {
		data[i] = byte(rand.Intn(256))
	}

	return data
}

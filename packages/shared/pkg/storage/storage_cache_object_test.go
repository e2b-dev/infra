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

	storagemocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks"
)

var noopTracer = noop.TracerProvider{}.Tracer("")

func TestCachedObjectProvider_WriteFromFileSystem(t *testing.T) {
	t.Parallel()

	t.Run("can be cached successfully", func(t *testing.T) {
		t.Parallel()

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

		featureFlags := storagemocks.NewMockFeatureFlagsClient(t)
		featureFlags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(true)

		c := CachedObjectProvider{path: cacheDir, inner: inner, chunkSize: 1024, flags: featureFlags, tracer: noopTracer}

		// write temp file
		err = c.WriteFromFileSystem(t.Context(), tempFilename)
		require.NoError(t, err)

		// file is written asynchronously, wait for it to finish
		c.wg.Wait()

		// prevent the provider from falling back to cache
		c.inner = nil

		var buff bytes.Buffer
		bytesRead, err := c.WriteTo(t.Context(), &buff)
		require.NoError(t, err)
		assert.Equal(t, data, buff.Bytes())
		assert.Equal(t, int64(len(data)), bytesRead)
	})

	t.Run("uncached reads will be cached the second time", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		err := os.MkdirAll(cacheDir, 0o777)
		require.NoError(t, err)

		const dataSize = 10 * megabyte
		actualData := generateData(t, dataSize)

		inner := storagemocks.NewMockObjectProvider(t)
		inner.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, w io.Writer) (int64, error) {
				num, err := w.Write(actualData)

				return int64(num), err
			})

		c := CachedObjectProvider{path: cacheDir, inner: inner, chunkSize: 1024, tracer: noopTracer}

		buff := bytes.NewBuffer(nil)
		bytesRead, err := c.WriteTo(t.Context(), buff)
		require.NoError(t, err)
		assert.Equal(t, int64(dataSize), bytesRead)
		assert.Equal(t, actualData, buff.Bytes())

		c.wg.Wait()

		c.inner = nil

		buff = bytes.NewBuffer(nil)
		bytesRead, err = c.WriteTo(t.Context(), buff)
		require.NoError(t, err)
		assert.Equal(t, int64(dataSize), bytesRead)
		assert.Equal(t, actualData, buff.Bytes())
	})
}

func TestCachedObjectProvider_WriteFileToCache(t *testing.T) {
	t.Parallel()

	c := CachedObjectProvider{
		path:   t.TempDir(),
		tracer: noopTracer,
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

func generateData(t *testing.T, count int) []byte {
	t.Helper()

	data := make([]byte, count)
	for i := range count {
		data[i] = byte(rand.Intn(256))
	}

	return data
}

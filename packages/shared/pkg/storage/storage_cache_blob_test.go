package storage

import (
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

func TestCachedObjectProvider_Put(t *testing.T) {
	t.Parallel()

	t.Run("can be cached successfully", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		data := []byte("hello world")

		err := os.MkdirAll(cacheDir, os.ModePerm)
		require.NoError(t, err)

		inner := storagemocks.NewMockBlob(t)
		inner.EXPECT().
			Put(mock.Anything, mock.Anything).
			Return(nil)

		featureFlags := storagemocks.NewMockFeatureFlagsClient(t)
		featureFlags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(true)

		c := cachedBlob{path: cacheDir, inner: inner, chunkSize: 1024, flags: featureFlags, tracer: noopTracer}

		// write temp file
		err = c.Put(t.Context(), data)
		require.NoError(t, err)

		// file is written asynchronously, wait for it to finish
		c.wg.Wait()

		// prevent the provider from falling back to cache
		c.inner = nil

		gotData, err := GetBlob(t.Context(), &c)
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

		inner := storagemocks.NewMockBlob(t)
		inner.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
				n, err := dst.Write(actualData)

				return int64(n), err
			})

		c := cachedBlob{path: cacheDir, inner: inner, chunkSize: 1024, tracer: noopTracer}

		read, err := GetBlob(t.Context(), &c)
		require.NoError(t, err)
		assert.Equal(t, actualData, read)

		c.wg.Wait()

		c.inner = nil

		read, err = GetBlob(t.Context(), &c)
		require.NoError(t, err)
		assert.Equal(t, actualData, read)
	})
}

func TestCachedObjectProvider_WriteFileToCache(t *testing.T) {
	t.Parallel()

	c := cachedBlob{
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

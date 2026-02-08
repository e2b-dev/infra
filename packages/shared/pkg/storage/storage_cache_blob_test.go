package storage

import (
	"bytes"
	"context"
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

func TestCachedStorage_Blobber(t *testing.T) {
	t.Parallel()

	t.Run("StoreBlob write-through caching", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		data := []byte("hello world")

		err := os.MkdirAll(cacheDir, os.ModePerm)
		require.NoError(t, err)

		inner := NewMockStorageProvider(t)
		inner.EXPECT().
			StoreBlob(mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

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
		wg, err := c.storeBlob(t.Context(), "test-item", bytes.NewReader(data))
		require.NoError(t, err)
		require.NotNil(t, wg)

		// file is written asynchronously, wait for it to finish
		wg.Wait()

		// prevent the storage provider from falling back to cache
		c.inner = nil

		gotData, wg, err := c.getBlob(t.Context(), "test-item")
		require.NoError(t, err)
		assert.Equal(t, data, gotData)

		wg.Wait()
	})

	apiWithData := func(t *testing.T, data []byte) *MockStorageProvider {
		t.Helper()

		inner := NewMockStorageProvider(t)
		inner.EXPECT().
			GetBlob(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _ string) ([]byte, error) {
				shadow := make([]byte, len(data))
				copy(shadow, data)

				return shadow, nil
			})

		return inner
	}

	t.Run("CopyBlob read-through caching", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		err := os.MkdirAll(cacheDir, 0o777)
		require.NoError(t, err)

		const dataSize = 10 * megabyte
		actualData := generateData(t, dataSize)
		c := &Cache{
			rootPath:  cacheDir,
			inner:     apiWithData(t, actualData),
			chunkSize: 1024,
			tracer:    noopTracer,
		}

		buf := bytes.NewBuffer(nil)
		read, wg, err := c.copyBlob(t.Context(), "test-item", buf)
		require.NoError(t, err)
		assert.Equal(t, int64(len(actualData)), read)
		assert.Equal(t, actualData, buf.Bytes())

		wg.Wait()

		c.inner = nil

		buf = bytes.NewBuffer(nil)
		read, wg, err = c.copyBlob(t.Context(), "test-item", buf)
		require.NoError(t, err)
		assert.Equal(t, int64(len(actualData)), read)
		assert.Equal(t, actualData, buf.Bytes())

		wg.Wait()
	})

	t.Run("GetBlob read-through caching", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		err := os.MkdirAll(cacheDir, 0o777)
		require.NoError(t, err)

		const dataSize = 10 * megabyte
		actualData := generateData(t, dataSize)

		c := &Cache{
			rootPath:  cacheDir,
			inner:     apiWithData(t, actualData),
			chunkSize: 1024,
			tracer:    noopTracer,
		}

		data, wg, err := c.getBlob(t.Context(), "test-item")
		require.NoError(t, err)
		assert.Len(t, data, len(actualData))
		assert.Equal(t, actualData, data)

		wg.Wait()

		c.inner = nil

		data, wg, err = c.getBlob(t.Context(), "test-item")
		require.NoError(t, err)
		assert.Equal(t, actualData, data)

		wg.Wait()
	})
}

func generateData(t *testing.T, count int) []byte {
	t.Helper()

	data := make([]byte, count)
	for i := range count {
		data[i] = byte(rand.Intn(256))
	}

	return data
}

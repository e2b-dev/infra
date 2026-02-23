package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCachedFramedFile_MakeChunkFilename(t *testing.T) {
	t.Parallel()

	c := cachedFramedFile{path: "/a/b/c", chunkSize: 1024, tracer: noopTracer}
	filename := c.makeChunkFilename(1024 * 4)
	assert.Equal(t, "/a/b/c/000000000004-1024.bin", filename)
}

func TestCachedFramedFile_Size(t *testing.T) {
	t.Parallel()

	t.Run("can be cached successfully", func(t *testing.T) {
		t.Parallel()

		const expectedSize int64 = 1024

		inner := NewMockFramedFile(t)
		inner.EXPECT().Size(mock.Anything).Return(expectedSize, nil)

		c := cachedFramedFile{path: t.TempDir(), inner: inner, tracer: noopTracer}

		// first call will write to cache
		size, err := c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)

		// sleep, cache writing is async
		c.wg.Wait()

		// second call must come from cache
		c.inner = nil

		size, err = c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)
	})
}

func TestCachedFramedFile_WriteFromFileSystem(t *testing.T) {
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

		inner := NewMockFramedFile(t)
		inner.EXPECT().
			StoreFile(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)

		featureFlags := NewMockFeatureFlagsClient(t)
		featureFlags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(true)
		featureFlags.EXPECT().IntFlag(mock.Anything, mock.Anything).Return(10)

		c := cachedFramedFile{path: cacheDir, inner: inner, chunkSize: 1024, flags: featureFlags, tracer: noopTracer}

		// write temp file
		_, err = c.StoreFile(t.Context(), tempFilename, nil)
		require.NoError(t, err)

		// file is written asynchronously, wait for it to finish
		c.wg.Wait()

		c.inner = nil

		// size should be cached
		size, err := c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, int64(len(data)), size)
	})
}

func TestCachedFramedFile_GetFrame_Uncompressed(t *testing.T) {
	t.Parallel()

	t.Run("cache hit from chunk file", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		tempPath := filepath.Join(tempDir, "a", "b", "c")
		c := cachedFramedFile{path: tempPath, chunkSize: 3, tracer: noopTracer}

		// create cache file
		cacheFilename := c.makeChunkFilename(0)
		dirName := filepath.Dir(cacheFilename)
		err := os.MkdirAll(dirName, 0o755)
		require.NoError(t, err)
		err = os.WriteFile(cacheFilename, []byte{1, 2, 3}, 0o600)
		require.NoError(t, err)

		buffer := make([]byte, 3)
		r, err := c.GetFrame(t.Context(), 0, nil, false, buffer, 0, nil)
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, buffer)
		assert.Equal(t, 3, r.Length)
	})

	t.Run("cache miss then write-back", func(t *testing.T) {
		t.Parallel()

		fakeData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		inner := NewMockFramedFile(t)
		inner.EXPECT().
			GetFrame(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, offsetU int64, _ *FrameTable, _ bool, buf []byte, _ int64, onRead func(int64)) (Range, error) {
				end := min(int(offsetU)+len(buf), len(fakeData))
				n := copy(buf, fakeData[offsetU:end])
				if onRead != nil {
					onRead(int64(n))
				}

				return Range{Start: offsetU, Length: n}, nil
			})

		tempDir := t.TempDir()
		c := cachedFramedFile{
			path:      tempDir,
			chunkSize: 3,
			inner:     inner,
			tracer:    noopTracer,
		}

		// first read goes to source
		buffer := make([]byte, 3)
		r, err := c.GetFrame(t.Context(), 3, nil, false, buffer, 0, nil)
		require.NoError(t, err)
		assert.Equal(t, []byte{4, 5, 6}, buffer[:r.Length])

		// wait for write-back
		c.wg.Wait()

		// second read from cache
		c.inner = nil
		buffer = make([]byte, 3)
		r, err = c.GetFrame(t.Context(), 3, nil, false, buffer, 0, nil)
		require.NoError(t, err)
		assert.Equal(t, []byte{4, 5, 6}, buffer[:r.Length])
	})
}

func TestCachedFramedFile_WriteTo(t *testing.T) {
	t.Parallel()

	t.Run("WriteTo calls should read from cache", func(t *testing.T) {
		t.Parallel()

		fakeData := []byte{1, 2, 3}

		fakeStorageObjectProvider := NewMockBlob(t)
		fakeStorageObjectProvider.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
				n, err := dst.Write(fakeData)

				return int64(n), err
			})

		tempDir := t.TempDir()
		c := cachedBlob{
			path:      tempDir,
			chunkSize: 3,
			inner:     fakeStorageObjectProvider,
			tracer:    noopTracer,
		}

		// write to both local and remote storage
		data, err := GetBlob(t.Context(), &c)
		require.NoError(t, err)
		assert.Equal(t, fakeData, data)

		// WriteTo is async, wait for the write to finish
		c.wg.Wait()

		// second read should go straight to local
		c.inner = nil
		data, err = GetBlob(t.Context(), &c)
		require.NoError(t, err)
		assert.Equal(t, fakeData, data)
	})
}

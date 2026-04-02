package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCachedSeekable_MakeChunkFilename(t *testing.T) {
	t.Parallel()

	c := cachedSeekable{path: "/a/b/c", chunkSize: 1024, tracer: noopTracer}
	filename := c.makeChunkFilename(1024 * 4)
	assert.Equal(t, "/a/b/c/000000000004-1024.bin", filename)
}

func TestCachedSeekable_Size(t *testing.T) {
	t.Parallel()

	t.Run("can be cached successfully", func(t *testing.T) {
		t.Parallel()

		const expectedSize int64 = 1024

		inner := NewMockSeekable(t)
		inner.EXPECT().Size(mock.Anything).Return(expectedSize, nil)

		c := cachedSeekable{path: t.TempDir(), inner: inner, tracer: noopTracer}

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

func TestCachedSeekable_WriteFromFileSystem(t *testing.T) {
	t.Parallel()

	t.Run("delegates to inner", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		cacheDir := filepath.Join(tempDir, "cache")
		tempFilename := filepath.Join(tempDir, "temp.bin")
		data := []byte("hello world")

		err := os.MkdirAll(cacheDir, os.ModePerm)
		require.NoError(t, err)

		err = os.WriteFile(tempFilename, data, 0o644)
		require.NoError(t, err)

		inner := NewMockSeekable(t)
		inner.EXPECT().
			StoreFile(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, [32]byte{}, nil)

		featureFlags := NewMockFeatureFlagsClient(t)
		featureFlags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(false)

		c := cachedSeekable{path: cacheDir, inner: inner, chunkSize: 1024, flags: featureFlags, tracer: noopTracer}

		_, _, err = c.StoreFile(t.Context(), tempFilename, nil)
		require.NoError(t, err)
	})
}

func TestCachedSeekable_OpenRangeReader_Uncompressed(t *testing.T) {
	t.Parallel()

	t.Run("cache hit from chunk file", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		tempPath := filepath.Join(tempDir, "a", "b", "c")
		c := cachedSeekable{path: tempPath, chunkSize: 3, tracer: noopTracer}

		// create cache file
		cacheFilename := c.makeChunkFilename(0)
		dirName := filepath.Dir(cacheFilename)
		err := os.MkdirAll(dirName, 0o755)
		require.NoError(t, err)
		err = os.WriteFile(cacheFilename, []byte{1, 2, 3}, 0o600)
		require.NoError(t, err)

		rc, err := c.OpenRangeReader(t.Context(), 0, 3, nil)
		require.NoError(t, err)
		defer rc.Close()

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, []byte{1, 2, 3}, got)
	})

	t.Run("cache miss then write-back", func(t *testing.T) {
		t.Parallel()

		fakeData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		inner := NewMockSeekable(t)
		inner.EXPECT().
			OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, offsetU int64, length int64, _ *FrameTable) (io.ReadCloser, error) {
				end := min(int(offsetU)+int(length), len(fakeData))

				return io.NopCloser(bytes.NewReader(fakeData[offsetU:end])), nil
			})

		tempDir := t.TempDir()
		c := cachedSeekable{
			path:      tempDir,
			chunkSize: 3,
			inner:     inner,
			tracer:    noopTracer,
		}

		// first read goes to source
		rc, err := c.OpenRangeReader(t.Context(), 3, 3, nil)
		require.NoError(t, err)
		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		require.Equal(t, []byte{4, 5, 6}, got)

		// wait for write-back
		c.wg.Wait()

		// second read from cache
		c.inner = nil
		rc, err = c.OpenRangeReader(t.Context(), 3, 3, nil)
		require.NoError(t, err)
		got, err = io.ReadAll(rc)
		require.NoError(t, err)
		rc.Close()
		require.Equal(t, []byte{4, 5, 6}, got)
	})
}

func TestCachedSeekable_OpenRangeReader_SkipWriteback(t *testing.T) {
	t.Parallel()

	fakeData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	inner := NewMockSeekable(t)
	inner.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, offsetU int64, length int64, _ *FrameTable) (io.ReadCloser, error) {
			end := min(int(offsetU)+int(length), len(fakeData))

			return io.NopCloser(bytes.NewReader(fakeData[offsetU:end])), nil
		})

	tempDir := t.TempDir()
	c := cachedSeekable{
		path:      tempDir,
		chunkSize: 10,
		inner:     inner,
		tracer:    noopTracer,
	}

	ctx := WithSkipCacheWriteback(t.Context())
	rc, err := c.OpenRangeReader(ctx, 0, 10, nil)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	rc.Close()
	require.Equal(t, fakeData, got)

	c.wg.Wait()

	chunkPath := c.makeChunkFilename(0)
	_, statErr := os.Stat(chunkPath)
	require.True(t, os.IsNotExist(statErr), "cache writeback should be skipped")
}

func TestCachedSeekable_WriteTo(t *testing.T) {
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

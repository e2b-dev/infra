package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	storagemocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks"
)

func TestCachedFileObjectProvider_MakeChunkFilename(t *testing.T) {
	t.Parallel()

	c := cachedSeekable{path: "/a/b/c", chunkSize: 1024, tracer: noopTracer}
	filename := c.makeChunkFilename(1024 * 4)
	assert.Equal(t, "/a/b/c/000000000004-1024.bin", filename)
}

func TestCachedFileObjectProvider_Size(t *testing.T) {
	t.Parallel()

	t.Run("can be cached successfully", func(t *testing.T) {
		t.Parallel()

		const expectedSize int64 = 1024

		inner := storagemocks.NewMockSeekable(t)
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

func TestCachedFileObjectProvider_WriteFromFileSystem(t *testing.T) {
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

		inner := storagemocks.NewMockSeekable(t)
		inner.EXPECT().
			StoreFile(mock.Anything, mock.Anything).
			Return(nil)

		featureFlags := storagemocks.NewMockFeatureFlagsClient(t)
		featureFlags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(true)
		featureFlags.EXPECT().IntFlag(mock.Anything, mock.Anything).Return(10)

		c := cachedSeekable{path: cacheDir, inner: inner, chunkSize: 1024, flags: featureFlags, tracer: noopTracer}

		// write temp file
		err = c.StoreFile(t.Context(), tempFilename)
		require.NoError(t, err)

		// file is written asynchronously, wait for it to finish
		c.wg.Wait()

		c.inner = nil

		// size should be cached
		size, err := c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, int64(len(data)), size)

		// verify that the size has been cached
		buff := make([]byte, len(data))
		bytesRead, err := c.ReadAt(t.Context(), buff, 0)
		require.NoError(t, err)
		assert.Equal(t, data, buff)
		assert.Equal(t, len(data), bytesRead)
	})
}

func TestCachedFileObjectProvider_WriteTo(t *testing.T) {
	t.Parallel()

	t.Run("read from cache when the file exists", func(t *testing.T) {
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

		buffer := make([]byte, 3)
		read, err := c.ReadAt(t.Context(), buffer, 0)
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, buffer)
		assert.Equal(t, 3, read)
	})

	t.Run("consecutive ReadAt calls should cache", func(t *testing.T) {
		t.Parallel()

		fakeData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		fakeStorageObjectProvider := storagemocks.NewMockSeekable(t)

		fakeStorageObjectProvider.EXPECT().
			ReadAt(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, buff []byte, off int64) (int, error) {
				start := off
				end := off + int64(len(buff))
				end = min(end, int64(len(fakeData)))
				copy(buff, fakeData[start:end])

				return int(end - start), nil
			})

		tempDir := t.TempDir()
		c := cachedSeekable{
			path:      tempDir,
			chunkSize: 3,
			inner:     fakeStorageObjectProvider,
			tracer:    noopTracer,
		}

		// first read goes to source
		buffer := make([]byte, 3)
		read, err := c.ReadAt(t.Context(), buffer, 3)
		require.NoError(t, err)
		assert.Equal(t, []byte{4, 5, 6}, buffer)
		assert.Equal(t, 3, read)

		// we write asynchronously, so let's wait until we're done
		c.wg.Wait()

		// second read pulls from cache
		c.inner = nil // prevent remote reads, force cache read
		buffer = make([]byte, 3)
		read, err = c.ReadAt(t.Context(), buffer, 3)
		require.NoError(t, err)
		assert.Equal(t, []byte{4, 5, 6}, buffer)
		assert.Equal(t, 3, read)
	})

	t.Run("WriteTo calls should read from cache", func(t *testing.T) {
		t.Parallel()

		fakeData := []byte{1, 2, 3}

		fakeStorageObjectProvider := storagemocks.NewMockBlob(t)
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

func TestCachedFileObjectProvider_validateReadAtParams(t *testing.T) {
	t.Parallel()

	testcases := map[string]struct {
		chunkSize, bufferSize, offset int64
		expected                      error
	}{
		"buffer is empty": {
			chunkSize:  1,
			bufferSize: 0,
			offset:     0,
			expected:   ErrBufferTooSmall,
		},
		"buffer is smaller than chunk size": {
			chunkSize:  10,
			bufferSize: 5,
			offset:     0,
		},
		"offset is unaligned": {
			chunkSize:  10,
			bufferSize: 10,
			offset:     3,
			expected:   ErrOffsetUnaligned,
		},
		"buffer is too large (unaligned)": {
			chunkSize:  10,
			bufferSize: 11,
			expected:   ErrBufferTooLarge,
		},
		"buffer is too large (aligned)": {
			chunkSize:  10,
			bufferSize: 20,
			expected:   ErrBufferTooLarge,
		},
	}

	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := cachedSeekable{
				chunkSize: tc.chunkSize,
				tracer:    noopTracer,
			}
			err := c.validateReadAtParams(tc.bufferSize, tc.offset)
			if tc.expected == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.expected)
			}
		})
	}
}

func TestCachedSeekableObjectProvider_ReadAt(t *testing.T) {
	t.Parallel()

	t.Run("failed but returns count on short read", func(t *testing.T) {
		t.Parallel()

		c := cachedSeekable{chunkSize: 10, tracer: noopTracer}
		errTarget := errors.New("find me")
		mockSeeker := storagemocks.NewMockSeekable(t)
		mockSeeker.EXPECT().ReadAt(mock.Anything, mock.Anything, mock.Anything).Return(5, errTarget)
		c.inner = mockSeeker

		buff := make([]byte, 10)
		count, err := c.ReadAt(t.Context(), buff, 0)
		require.ErrorIs(t, err, errTarget)
		assert.Equal(t, 5, count)
	})
}

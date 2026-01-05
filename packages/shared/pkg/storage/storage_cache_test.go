package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCachedFileObjectProvider_MakeChunkFilename(t *testing.T) {
	c := cachedFramedReaderWriter{path: "/a/b/c", chunkSize: 1024}
	filename := c.makeChunkFilename(1024 * 4)
	assert.Equal(t, "/a/b/c/000000000004-1024.bin", filename)
}

func TestCachedFileObjectProvider_Size(t *testing.T) {
	t.Run("can be cached successfully", func(t *testing.T) {
		const expectedSize int64 = 1024

		inner := NewMockFramedReader(t)
		inner.EXPECT().Size(mock.Anything).Return(expectedSize, nil)

		c := cachedFramedReaderWriter{path: t.TempDir(), r: inner}

		// first call will write to cache
		size, err := c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)

		// sleep, cache writing is async
		time.Sleep(20 * time.Millisecond)

		// second call must come from cache
		c.r = nil

		size, err = c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)
	})
}

func TestCachedFileObjectProvider_WriteTo(t *testing.T) {
	t.Run("read from cache when the file exists", func(t *testing.T) {
		tempDir := t.TempDir()

		tempPath := filepath.Join(tempDir, "a", "b", "c")
		c := cachedFramedReaderWriter{path: tempPath, chunkSize: 3}

		// create cache file
		cacheFilename := c.makeChunkFilename(0)
		dirName := filepath.Dir(cacheFilename)
		err := os.MkdirAll(dirName, 0o755)
		require.NoError(t, err)
		err = os.WriteFile(cacheFilename, []byte{1, 2, 3}, 0o600)
		require.NoError(t, err)

		start, frames, err := c.ReadFrames(t.Context(), 0, 3, nil)
		require.NoError(t, err)
		require.Len(t, frames, 1)
		require.Equal(t, int64(0), start)
		assert.Equal(t, []byte{1, 2, 3}, frames[0])
	})

	t.Run("consecutive ReadAt calls should cache", func(t *testing.T) {
		fakeData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		fakeStorageObjectProvider := NewMockFramedReader(t)

		fakeStorageObjectProvider.EXPECT().
			ReadFrames(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, off int64, n int, _ *FrameTable) (int64, [][]byte, error) {
				start := off
				end := off + int64(n)
				end = min(end, int64(len(fakeData)))

				return off, [][]byte{fakeData[start:end]}, nil
			})

		tempDir := t.TempDir()
		c := cachedFramedReaderWriter{
			path:      tempDir,
			chunkSize: 3,
			r:         fakeStorageObjectProvider,
		}

		// first read goes to source
		start, frames, err := c.ReadFrames(t.Context(), 3, 3, nil)
		require.NoError(t, err)
		assert.Equal(t, int64(3), start)
		assert.Equal(t, []byte{4, 5, 6}, frames[0])

		// we write asynchronously, so let's wait until we're done
		time.Sleep(time.Millisecond * 20)

		// second read pulls from cache
		c.r = nil // prevent remote reads, force cache read
		start, frames, err = c.ReadFrames(t.Context(), 3, 3, nil)
		require.NoError(t, err)
		assert.Equal(t, int64(3), start)
		assert.Equal(t, []byte{4, 5, 6}, frames[0])
	})

	t.Run("WriteTo calls should read from cache", func(t *testing.T) {
		fakeData := []byte{1, 2, 3}

		fakeStorageObjectProvider := NewMockObjectProvider(t)
		fakeStorageObjectProvider.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, dst io.Writer) (int64, error) {
				num, err := dst.Write(fakeData)

				return int64(num), err
			})

		tempDir := t.TempDir()
		c := cachedObject{
			path:      tempDir,
			chunkSize: 3,
			inner:     fakeStorageObjectProvider,
		}

		// write to both local and remote storage
		var buffer bytes.Buffer
		count, err := c.WriteTo(t.Context(), &buffer)
		require.NoError(t, err)
		assert.Equal(t, int64(len(fakeData)), count)

		// WriteTo is async, wait for the write to finish
		time.Sleep(time.Millisecond * 20)

		// second read should go straight to local
		c.inner = nil
		var buff2 bytes.Buffer
		count, err = c.WriteTo(t.Context(), &buff2)
		require.NoError(t, err)
		assert.Equal(t, int64(len(fakeData)), count)
	})
}

func TestCachedFileObjectProvider_validateReadAtParams(t *testing.T) {
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
			c := cachedFramedReaderWriter{
				chunkSize: tc.chunkSize,
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

func TestMoveWithoutReplace_SuccessWhenDestMissing(t *testing.T) {
	ctx := t.Context()
	td := t.TempDir()
	content := []byte("alpha")
	src := filepath.Join(td, "src")
	dst := filepath.Join(td, "dst")

	require.NoError(t, os.WriteFile(src, content, 0o644))
	err := moveWithoutReplace(ctx, src, dst)
	require.NoError(t, err)

	// Dest has original content.
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	_, err = os.Stat(src)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMoveWithoutReplace_FailWhenExists(t *testing.T) {
	ctx := t.Context()
	td := t.TempDir()
	content := []byte("alpha")
	secondContent := []byte("beta")
	src := filepath.Join(td, "src")
	dst := filepath.Join(td, "dst")

	require.NoError(t, os.WriteFile(src, content, 0o644))
	require.NoError(t, os.WriteFile(dst, secondContent, 0o644))
	err := moveWithoutReplace(ctx, src, dst)
	require.NoError(t, err)

	// Dest has original content.
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, secondContent, got)

	_, err = os.Stat(src)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

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

	storagemocks "github.com/e2b-dev/infra/packages/shared/pkg/storage/mocks"
)

func TestCachedFileObjectProvider_MakeChunkFilename(t *testing.T) {
	c := CachedFileObjectProvider{path: "/a/b/c", chunkSize: 1024}
	filename := c.makeChunkFilename(1024 * 4)
	assert.Equal(t, "/a/b/c/000000000004-1024.bin", filename)
}

func TestCachedFileObjectProvider_Size(t *testing.T) {
	t.Run("can be cached successfully", func(t *testing.T) {
		const expectedSize int64 = 1024

		inner := storagemocks.NewMockStorageObjectProvider(t)
		inner.EXPECT().Size(mock.Anything).Return(expectedSize, nil)

		c := CachedFileObjectProvider{path: t.TempDir(), inner: inner}

		// first call will write to cache
		size, err := c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)

		// sleep, cache writing is async
		time.Sleep(20 * time.Millisecond)

		// second call must come from cache
		c.inner = nil

		size, err = c.Size(t.Context())
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)
	})
}

func TestCachedFileObjectProvider_WriteTo(t *testing.T) {
	t.Run("read from cache when the file exists", func(t *testing.T) {
		tempDir := t.TempDir()

		tempPath := filepath.Join(tempDir, "a", "b", "c")
		c := CachedFileObjectProvider{path: tempPath, chunkSize: 3}

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
		fakeData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		fakeStorageObjectProvider := storagemocks.NewMockStorageObjectProvider(t)

		fakeStorageObjectProvider.EXPECT().
			ReadAt(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(ctx context.Context, buff []byte, off int64) (int, error) {
				start := off
				end := off + int64(len(buff))
				end = min(end, int64(len(fakeData)))
				copy(buff, fakeData[start:end])
				return int(end - start), nil
			})

		tempDir := t.TempDir()
		c := CachedFileObjectProvider{
			path:      tempDir,
			chunkSize: 3,
			inner:     fakeStorageObjectProvider,
		}

		// first read goes to source
		buffer := make([]byte, 3)
		read, err := c.ReadAt(t.Context(), buffer, 3)
		require.NoError(t, err)
		assert.Equal(t, []byte{4, 5, 6}, buffer)
		assert.Equal(t, 3, read)

		// we write asynchronously, so let's wait until we're done
		time.Sleep(time.Millisecond * 20)

		// second read pulls from cache
		c.inner = nil // prevent remote reads, force cache read
		buffer = make([]byte, 3)
		read, err = c.ReadAt(t.Context(), buffer, 3)
		require.NoError(t, err)
		assert.Equal(t, []byte{4, 5, 6}, buffer)
		assert.Equal(t, 3, read)
	})

	t.Run("WriteTo calls should read from cache", func(t *testing.T) {
		fakeData := []byte{1, 2, 3}

		fakeStorageObjectProvider := storagemocks.NewMockStorageObjectProvider(t)
		fakeStorageObjectProvider.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			RunAndReturn(func(ctx context.Context, dst io.Writer) (int64, error) {
				num, err := dst.Write(fakeData)
				return int64(num), err
			})

		tempDir := t.TempDir()
		c := CachedFileObjectProvider{
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
			c := CachedFileObjectProvider{
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
	td := t.TempDir()
	content := []byte("alpha")
	src := filepath.Join(td, "src")
	dst := filepath.Join(td, "dst")

	require.NoError(t, os.WriteFile(src, content, 0o644))
	err := moveWithoutReplace(src, dst)
	require.NoError(t, err)

	// Dest has original content.
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	_, err = os.Stat(src)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMoveWithoutReplace_FailWhenExists(t *testing.T) {
	td := t.TempDir()
	content := []byte("alpha")
	secondContent := []byte("beta")
	src := filepath.Join(td, "src")
	dst := filepath.Join(td, "dst")

	require.NoError(t, os.WriteFile(src, content, 0o644))
	require.NoError(t, os.WriteFile(dst, secondContent, 0o644))
	err := moveWithoutReplace(src, dst)
	require.NoError(t, err)

	// Dest has original content.
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, secondContent, got)

	_, err = os.Stat(src)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestMoveWithoutReplace_Fail(t *testing.T) {
	td := t.TempDir()
	content := []byte("alpha")
	src := filepath.Join(td, "src")
	require.NoError(t, os.WriteFile(src, content, 0o644))

	roDir := filepath.Join(td, "ro")
	require.NoError(t, os.Mkdir(roDir, 0o555)) // r-x only, no write
	t.Cleanup(func() {
		// ensure cleanup possible
		err := os.Chmod(roDir, 0o755)
		assert.NoError(t, err)
	})

	dst := filepath.Join(roDir, "dst")
	err := moveWithoutReplace(src, dst)
	require.Error(t, err)

	_, err = os.Stat(src)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

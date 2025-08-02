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

func TestCachedFileObjectProvider_MakeChunkFilename(t *testing.T) {
	c := CachedFileObjectProvider{path: "/a/b/c", chunkSize: 1024}
	filename := c.makeChunkFilename(1024 * 4)
	assert.Equal(t, "/a/b/c/000000000004-1024.bin", filename)
}

func TestCachedFileObjectProvider_WriteTo(t *testing.T) {
	t.Run("prevent unaligned reads", func(t *testing.T) {
	})

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
		fakeStorageObjectProvider := NewMockStorageObjectProvider(t)

		fakeStorageObjectProvider.EXPECT().
			ReadAt(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(ctx context.Context, buff []byte, off int64) (int, error) {
				start := off
				end := off + int64(len(buff))
				end = min(end, int64(len(fakeData)))
				copy(buff[:], fakeData[start:end])
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

		fakeStorageObjectProvider := NewMockStorageObjectProvider(t)
		fakeStorageObjectProvider.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			RunAndReturn(func(ctx context.Context, dst io.Writer) (int64, error) {
				num, err := dst.Write(fakeData)
				return int64(num), err
			})
		fakeStorageObjectProvider.EXPECT().
			Size(mock.Anything).Return(int64(len(fakeData)), nil)

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

		// second read should go straight to local, although it grabs the size
		fakeStorageObjectProvider.EXPECT().
			WriteTo(mock.Anything, mock.Anything).
			Panic("something bad happened")
		var buff2 bytes.Buffer
		count, err = c.WriteTo(t.Context(), &buff2)
		require.NoError(t, err)
		assert.Equal(t, int64(len(fakeData)), count)
	})

	t.Run("WriteFromFileSystem should write to cache", func(t *testing.T) {
		fakeData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

		fakeStorageObjectProvider := NewMockStorageObjectProvider(t)
		fakeStorageObjectProvider.
			EXPECT().
			WriteFromFileSystem(mock.Anything, mock.Anything).
			Return(nil)

		tempDir := t.TempDir()
		c := CachedFileObjectProvider{
			path:      tempDir,
			chunkSize: 3,
			inner:     fakeStorageObjectProvider,
		}

		// create temp file
		inputFile := filepath.Join(tempDir, "tempfile.bin")
		err := os.WriteFile(inputFile, fakeData, 0o644)
		require.NoError(t, err)

		// write file to object store
		err = c.WriteFromFileSystem(t.Context(), inputFile)
		require.NoError(t, err)

		// read the object back, ensure remote is not called
		c.inner = nil
		buffer := make([]byte, 3)
		read, err := c.ReadAt(t.Context(), buffer, 0)
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, buffer)
		assert.Equal(t, 3, read)
	})

	t.Run("ReadFrom should read from cache", func(t *testing.T) {
		fakeData := []byte{1, 2, 3}

		fakeStorageObjectProvider := NewMockStorageObjectProvider(t)
		fakeStorageObjectProvider.EXPECT().
			ReadFrom(mock.Anything, mock.Anything).
			RunAndReturn(func(ctx context.Context, src []byte) (int64, error) {
				return int64(len(src)), nil
			})

		tempDir := t.TempDir()
		c := CachedFileObjectProvider{
			path:      tempDir,
			chunkSize: 3,
			inner:     fakeStorageObjectProvider,
		}

		read, err := c.ReadFrom(t.Context(), fakeData)
		require.NoError(t, err)
		assert.Equal(t, int64(len(fakeData)), read)

		buf := make([]byte, 3)
		read2, err := c.ReadAt(t.Context(), buf, 0)
		require.NoError(t, err)
		assert.Equal(t, fakeData, buf)
		assert.Equal(t, 3, read2)
	})
}

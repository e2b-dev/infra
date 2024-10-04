package cache

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMmapedFile(t *testing.T) {
	blockSize := int64(4096)

	size := int64(20 * blockSize)
	filePath := "test_mmap.dat"

	cache, err := NewMmapCache(size, blockSize, filePath)
	require.NoError(t, err, "error creating mmapedFile")
	defer cache.Close()
	defer os.Remove(filePath)

	// Test writing to the mmapedFile
	data := []byte("Hello, World!")
	offset := int64(100)
	n, err := cache.WriteAt(data, offset)
	assert.NoError(t, err, "error writing to mmapedFile")
	assert.Equal(t, len(data), n, "expected to write %d bytes, but wrote %d bytes", len(data), n)

	// Test reading from the mmapedFile
	readData := make([]byte, len(data))
	n, err = cache.ReadAt(readData, offset)
	assert.NoError(t, err, "error reading from mmapedFile")
	assert.Equal(t, len(data), n, "expected to read %d bytes, but read %d bytes", len(data), n)
	assert.True(t, bytes.Equal(data, readData), "expected to read %s, but read %s", data, readData)

	// Test writing and reading at different offsets
	data2 := []byte("Additional data")
	offset2 := int64(1000)
	n, err = cache.WriteAt(data2, offset2)
	assert.NoError(t, err, "error writing to mmapedFile")
	assert.Equal(t, len(data2), n, "expected to write %d bytes, but wrote %d bytes", len(data2), n)

	readData2 := make([]byte, len(data2))
	n, err = cache.ReadAt(readData2, offset2)
	assert.NoError(t, err, "error reading from mmapedFile")
	assert.Equal(t, len(data2), n, "expected to read %d bytes, but read %d bytes", len(data2), n)
	assert.True(t, bytes.Equal(data2, readData2), "expected to read %s, but read %s", data2, readData2)
}

func TestMmap2(t *testing.T) {
	blockSize := int64(4096)
	size := int64(20 * blockSize)
	filePath := "test_mmap.dat"

	mmap, err := NewMmapCache(size, blockSize, filePath)
	require.NoError(t, err, "Failed to create Mmap cache")
	defer mmap.Close()
	defer os.Remove(filePath)

	data := []byte("Hello, World!")
	off := int64(0)

	// Write data to the cache
	n, err := mmap.WriteAt(data, off)
	require.NoError(t, err, "Failed to write data")
	assert.Equal(t, len(data), n, "Wrote %d bytes, expected %d bytes", n, len(data))

	// Read data from the cache
	readData := make([]byte, len(data))
	n, err = mmap.ReadAt(readData, off)
	require.NoError(t, err, "Failed to read data")
	assert.Equal(t, len(data), n, "Read %d bytes, expected %d bytes", n, len(data))
	assert.Equal(t, string(data), string(readData), "Read data mismatch")

	// Read from unmarked offset
	_, err = mmap.ReadAt(readData, size)
	assert.Error(t, err, "Expected error for reading from unmarked offset")

	// Check if the offset is marked
	assert.True(t, mmap.marker.IsMarked(off), "Offset should be marked after writing")
	assert.False(t, mmap.marker.IsMarked(size), "Offset should not be marked")
}

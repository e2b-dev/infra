package cache

import (
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMmapCache(t *testing.T) {
	size := int64(20 * block.Size)
	filePath := "test_mmap.dat"

	mmap, err := NewMmapCache(size, filePath)
	require.NoError(t, err, "Failed to create Mmap cache")
	defer mmap.Close()
	defer os.Remove(filePath)

	assert.NotNil(t, mmap.mmap, "mmap field is nil")
	assert.NotNil(t, mmap.marker, "marker field is nil")
}

func TestMmap(t *testing.T) {
	size := int64(20 * block.Size)
	filePath := "test_mmap.dat"

	mmap, err := NewMmapCache(size, filePath)
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
	assert.True(t, mmap.IsMarked(off), "Offset should be marked after writing")
	assert.False(t, mmap.IsMarked(size), "Offset should not be marked")
}

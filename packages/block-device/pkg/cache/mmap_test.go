package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMmappedFile(t *testing.T) {
	// Test creating a new mmapped

	name := filepath.Join(os.TempDir(), "mmap_test")
	size := int64(1024 * block.Size)

	mmapedFile, err := newMmappedFile(size, name, true)
	require.NoError(t, err)
	defer mmapedFile.Close()
	defer os.Remove(name)

	// Check if the file was created and has the correct size
	fileInfo, err := os.Stat(name)
	require.NoError(t, err)
	assert.Equal(t, size, fileInfo.Size())
}

func TestMmapedFile_ReadWriteAt(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "mmap_test")
	require.NoError(t, err)
	defer os.Remove(file.Name())
	defer file.Close()

	// Allocate space for the file using fallocate
	size := int64(1024 * 1024) // 1 MB
	err = fallocate(size, file)
	require.NoError(t, err)

	// Create an mmapped file
	mmapedFile, err := newMmappedFile(size, file.Name(), false)
	require.NoError(t, err)
	defer mmapedFile.Close()

	// Test writing to the mmapped file
	data := []byte("Hello, World!")
	offset := int64(100)
	n, err := mmapedFile.WriteAt(data, offset)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)

	// Test reading from the mmapped file
	readData := make([]byte, len(data))
	n, err = mmapedFile.ReadAt(readData, offset)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, readData)
}

func TestMmapedFile_Close(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "mmap_test")
	require.NoError(t, err)
	defer os.Remove(file.Name())
	defer file.Close()

	// Create an mmapped file
	size := int64(1024 * 1024) // 1 MB
	mmapedFile, err := newMmappedFile(size, file.Name(), true)
	require.NoError(t, err)

	// Close the mmapped file
	err = mmapedFile.Close()
	assert.NoError(t, err)
}

// Add test for .Slice

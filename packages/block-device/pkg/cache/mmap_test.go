package cache

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMmapedFile(t *testing.T) {
	// Create a temporary file for testing
	tempFile, err := os.CreateTemp("", "mmap_test")
	require.NoError(t, err, "error creating temp file")
	defer os.Remove(tempFile.Name())

	// Create a new mmapedFile instance
	size := int64(1024)
	mf, err := newMmappedFile(size, tempFile.Name(), true)
	require.NoError(t, err, "error creating mmapedFile")
	defer mf.Close()

	// Test writing to the mmapedFile
	data := []byte("Hello, World!")
	offset := int64(100)
	n, err := mf.WriteAt(data, offset)
	assert.NoError(t, err, "error writing to mmapedFile")
	assert.Equal(t, len(data), n, "expected to write %d bytes, but wrote %d bytes", len(data), n)

	// Test reading from the mmapedFile
	readData := make([]byte, len(data))
	n, err = mf.ReadAt(readData, offset)
	assert.NoError(t, err, "error reading from mmapedFile")
	assert.Equal(t, len(data), n, "expected to read %d bytes, but read %d bytes", len(data), n)
	assert.True(t, bytes.Equal(data, readData), "expected to read %s, but read %s", data, readData)

	// Test reading from an invalid offset
	invalidOffset := size + 1
	_, err = mf.ReadAt(readData, invalidOffset)
	assert.Error(t, err, "expected an error when reading from an invalid offset")

	// Test writing to an invalid offset
	_, err = mf.WriteAt(data, invalidOffset)
	assert.Error(t, err, "expected an error when writing to an invalid offset")
}

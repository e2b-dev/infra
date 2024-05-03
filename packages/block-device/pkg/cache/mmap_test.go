package cache

import (
	"bytes"
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMmapedFile(t *testing.T) {
	file, err := os.CreateTemp("", "mmap_file_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	size := int64(32 * block.Size)
	err = file.Truncate(size)
	assert.NoError(t, err)

	mf, err := newMmappedFile(size, file.Name())
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

	// Test writing and reading at different offsets
	data2 := []byte("Additional data")
	offset2 := int64(1000)
	n, err = mf.WriteAt(data2, offset2)
	assert.NoError(t, err, "error writing to mmapedFile")
	assert.Equal(t, len(data2), n, "expected to write %d bytes, but wrote %d bytes", len(data2), n)

	readData2 := make([]byte, len(data2))
	n, err = mf.ReadAt(readData2, offset2)
	assert.NoError(t, err, "error reading from mmapedFile")
	assert.Equal(t, len(data2), n, "expected to read %d bytes, but read %d bytes", len(data2), n)
	assert.True(t, bytes.Equal(data2, readData2), "expected to read %s, but read %s", data2, readData2)
}

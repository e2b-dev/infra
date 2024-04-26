package cache

import (
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSparseFileView_CheckMarked(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "sparse_file_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	size := int64(32 * block.Size)
	err = file.Truncate(size)
	assert.NoError(t, err)
	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 1: No marked blocks
	marked, err := view.IsMarked(0)
	assert.False(t, marked)
	assert.NoError(t, err)
}

func TestSparseFileView_CheckMarked_MarkedBlockAtOffset0(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "sparse_file_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	size := int64(32 * block.Size)
	err = file.Truncate(size)
	assert.NoError(t, err)
	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 2: Marked block at offset 0
	_, err = file.WriteAt([]byte{1}, 0)
	require.NoError(t, err)
	marked, err := view.IsMarked(0)
	assert.True(t, marked)
	assert.NoError(t, err)
}

func TestSparseFileView_CheckMarked_MarkedBlockAtOffsetBlockSize(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "sparse_file_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	size := int64(32 * block.Size)
	err = file.Truncate(size)
	assert.NoError(t, err)
	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 3: Marked block at offset block.Size
	_, err = file.WriteAt([]byte{1}, block.Size)
	require.NoError(t, err)
	marked, err := view.IsMarked(block.Size)
	assert.True(t, marked)
	assert.NoError(t, err)
}

func TestSparseFileView_CheckMarked_OffsetInMiddleOfMarkedBlock(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "sparse_file_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	size := int64(32 * block.Size)
	err = file.Truncate(size)
	assert.NoError(t, err)
	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 4: Offset in the middle of a marked block
	_, err = file.WriteAt([]byte{1}, block.Size)
	require.NoError(t, err)
	marked, err := view.IsMarked(block.Size)
	assert.True(t, marked)
	assert.NoError(t, err)
}

func TestSparseFileView_CheckMarked_OffsetOverTwoMarkedBlocks(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "sparse_file_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	size := int64(32 * block.Size)
	err = file.Truncate(size)
	assert.NoError(t, err)
	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 5: Offset over two marked blocks
	data := make([]byte, 2*block.Size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	_, err = file.WriteAt(data, block.Size/2+block.Size)
	require.NoError(t, err)
	marked, err := view.IsMarked(block.Size * 2)
	assert.True(t, marked)
	assert.NoError(t, err)
}

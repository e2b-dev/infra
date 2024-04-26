package cache

import (
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSparseFileView_MarkedBlockRange(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "sparse_file_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	size := int64(32 * block.Size)
	err = fallocate(size, file)
	assert.NoError(t, err)
	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 1: No marked blocks
	start, end, err := view.MarkedBlockRange(0)
	assert.Equal(t, int64(0), start)
	assert.Equal(t, int64(0), end)
	assert.ErrorIs(t, err, ErrNoMarkFound{})

	// Test case 2: Marked block at offset 0
	err = fallocate(size, file)
	assert.NoError(t, err)
	_, err = file.WriteAt([]byte{1}, 0)
	require.NoError(t, err)
	start, end, err = view.MarkedBlockRange(0)
	assert.Equal(t, int64(0), start)
	assert.Equal(t, 1*block.Size-1, end)
	assert.NoError(t, err)

	// Test case 3: Marked block at offset block.Size
	err = fallocate(size, file)
	assert.NoError(t, err)
	_, err = file.WriteAt([]byte{1}, block.Size)
	require.NoError(t, err)
	start, end, err = view.MarkedBlockRange(block.Size)
	assert.Equal(t, block.Size, start)
	assert.Equal(t, block.Size*2-1, end)
	assert.NoError(t, err)

	// Test case 4: Offset in the middle of a marked block
	err = fallocate(size, file)
	assert.NoError(t, err)
	_, err = file.WriteAt([]byte{1}, block.Size/2+block.Size)
	require.NoError(t, err)
	start, end, err = view.MarkedBlockRange(block.Size)
	assert.Equal(t, block.Size, start)
	assert.Equal(t, block.Size*2-1, end)
	assert.NoError(t, err)

	// Test case 5: Offset over two marked blocks
	err = fallocate(size, file)
	assert.NoError(t, err)

	data := make([]byte, 2*block.Size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	_, err = file.WriteAt(data, block.Size/2+block.Size)
	require.NoError(t, err)
	start, end, err = view.MarkedBlockRange(block.Size * 2)
	assert.Equal(t, block.Size*2, start)
	assert.Equal(t, block.Size*4-1, end)
	assert.NoError(t, err)

	// Test case 6: Offset beyond the file size
	err = fallocate(size, file)
	start, end, err = view.MarkedBlockRange(size + 1)
	assert.Equal(t, int64(0), start)
	assert.Equal(t, int64(0), end)
	assert.ErrorIs(t, err, ErrNoMarkFound{})
}

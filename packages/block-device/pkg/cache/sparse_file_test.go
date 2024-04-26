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
	defer os.Remove(file.Name())
	defer file.Close()

	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 1: No marked blocks
	start, end, err := view.MarkedBlockRange(0)
	assert.Equal(t, int64(0), start)
	assert.Equal(t, int64(0), end)
	assert.ErrorIs(t, err, ErrNoMarkFound{})

	// Test case 2: Marked block at offset 0
	err = fallocate(block.Size, file)
	require.NoError(t, err)
	start, end, err = view.MarkedBlockRange(0)
	assert.Equal(t, int64(0), start)
	assert.Equal(t, block.Size-1, end)
	assert.NoError(t, err)

	// Test case 3: Marked block at offset block.Size
	err = fallocate(block.Size*2, file)
	require.NoError(t, err)
	start, end, err = view.MarkedBlockRange(block.Size)
	assert.Equal(t, block.Size, start)
	assert.Equal(t, block.Size*2-1, end)
	assert.NoError(t, err)

	// Test case 4: Offset in the middle of a marked block
	start, end, err = view.MarkedBlockRange(block.Size / 2)
	assert.Equal(t, int64(0), start)
	assert.Equal(t, block.Size-1, end)
	assert.NoError(t, err)

	// Test case 5: Offset beyond the file size
	start, end, err = view.MarkedBlockRange(block.Size * 3)
	assert.Equal(t, int64(0), start)
	assert.Equal(t, int64(0), end)
	assert.ErrorIs(t, err, ErrNoMarkFound{})
}

func TestSparseFileView_FirstUnmarked(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "sparse_file_test")
	require.NoError(t, err)
	defer os.Remove(file.Name())
	defer file.Close()

	// Create a sparse file view
	view := NewSparseFileView(file)

	// Test case 1: No marked blocks
	offset, err := view.firstUnmarked(0)
	assert.Equal(t, int64(0), offset)
	assert.NoError(t, err)

	// Test case 2: Marked block at offset 0
	err = fallocate(block.Size, file)
	require.NoError(t, err)
	offset, err = view.firstUnmarked(0)
	assert.Equal(t, block.Size, offset)
	assert.NoError(t, err)

	// Test case 3: Marked block at offset block.Size
	err = fallocate(block.Size*2, file)
	require.NoError(t, err)
	offset, err = view.firstUnmarked(block.Size)
	assert.Equal(t, block.Size*2, offset)
	assert.NoError(t, err)
}

// Test case 4: Offset beyond the file size

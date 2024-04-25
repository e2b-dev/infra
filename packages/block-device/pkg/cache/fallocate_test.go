package cache

import (
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestFallocate(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "fallocate_test")
	require.NoError(t, err)
	defer os.Remove(file.Name())
	defer file.Close()

	// Test fallocate with a specific size
	size := int64(1024 * block.Size) // 1 MB
	err = fallocate(size, file)
	assert.NoError(t, err)

	// Check the file size using fstat
	var stat unix.Stat_t
	err = unix.Fstat(int(file.Fd()), &stat)
	require.NoError(t, err)
	assert.Equal(t, size, stat.Size)

	// Test fallocate with a different size
	newSize := int64(2 * 1024 * block.Size) // 2 MB
	err = fallocate(newSize, file)
	assert.NoError(t, err)

	// Check the updated file size using fstat
	err = unix.Fstat(int(file.Fd()), &stat)
	require.NoError(t, err)
	assert.Equal(t, newSize, stat.Size)
}

package cache

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestFallocate(t *testing.T) {
	// Create a temporary file for testing
	file, err := os.CreateTemp("", "fallocate_test")
	require.NoError(t, err)
	defer file.Close()
	defer os.Remove(file.Name())

	// Test fallocate with a specific number of blocks
	// numBlocks := int64(100)
	size := int64(4096)
	err = fallocate(size, file)
	assert.NoError(t, err)

	// Check the file size using fstat
	var stat unix.Stat_t
	err = unix.Fstat(int(file.Fd()), &stat)
	require.NoError(t, err)
	assert.EqualValues(t, 0, stat.Size)

	fmt.Printf("stat.Blocks: %d, stat.Blksize: %d\n", stat.Blocks, stat.Blksize)

	// Test writing to the file increases the size
	data := []byte("Hello, World!")
	_, err = file.Write(data)
	require.NoError(t, err)

	err = unix.Fstat(int(file.Fd()), &stat)
	require.NoError(t, err)
	assert.EqualValues(t, len(data), stat.Size)
}

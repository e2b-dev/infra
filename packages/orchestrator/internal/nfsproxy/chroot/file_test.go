package chroot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestWrappedFile_LockUnlock(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.lock")

	f1, err := os.Create(filePath)
	require.NoError(t, err)
	t.Cleanup(func() {
		err := f1.Close()
		assert.NoError(t, err)
	})

	w1 := maybeWrap(f1)
	require.NotNil(t, w1)

	// Test basic Lock and Unlock
	err = w1.Lock()
	assert.NoError(t, err)

	err = w1.Unlock()
	assert.NoError(t, err)

	// Test exclusive locking
	err = w1.Lock()
	assert.NoError(t, err)

	f2, err := os.OpenFile(filePath, os.O_RDWR, 0o666)
	require.NoError(t, err)
	defer f2.Close()

	w2 := maybeWrap(f2)
	require.NotNil(t, w2)

	// Non-blocking lock on second handle should fail
	err = unix.Flock(int(f2.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	assert.ErrorIs(t, err, unix.EWOULDBLOCK)

	// Unlock first handle
	err = w1.Unlock()
	assert.NoError(t, err)

	// Now locking second handle should succeed
	err = w2.Lock()
	assert.NoError(t, err)

	err = w2.Unlock()
	assert.NoError(t, err)
}

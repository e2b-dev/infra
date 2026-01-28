package fileio

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalFile_SeekAndRead(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file with known content
	err := os.WriteFile(filepath.Join(tmpDir, "seek_test.txt"), []byte("hello world"), 0o644)
	require.NoError(t, err)

	file, err := fs.Open("seek_test.txt")
	require.NoError(t, err)
	defer file.Close()

	// Seek to position 6
	pos, err := file.Seek(6, io.SeekStart)
	require.NoError(t, err)
	assert.Equal(t, int64(6), pos)

	// Read from position 6
	buf := make([]byte, 5)
	n, err := file.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "world", string(buf))
}

func TestLocalFile_SeekWhence(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file with known content (11 bytes)
	err := os.WriteFile(filepath.Join(tmpDir, "seek_whence.txt"), []byte("hello world"), 0o644)
	require.NoError(t, err)

	file, err := fs.Open("seek_whence.txt")
	require.NoError(t, err)
	defer file.Close()

	// SeekStart
	pos, err := file.Seek(5, io.SeekStart)
	require.NoError(t, err)
	assert.Equal(t, int64(5), pos)

	// SeekCurrent (+2 from position 5 = 7)
	pos, err = file.Seek(2, io.SeekCurrent)
	require.NoError(t, err)
	assert.Equal(t, int64(7), pos)

	// SeekEnd (-3 from end = 8)
	pos, err = file.Seek(-3, io.SeekEnd)
	require.NoError(t, err)
	assert.Equal(t, int64(8), pos)
}

func TestLocalFile_ReadAt(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	err := os.WriteFile(filepath.Join(tmpDir, "readat.txt"), []byte("hello world"), 0o644)
	require.NoError(t, err)

	file, err := fs.Open("readat.txt")
	require.NoError(t, err)
	defer file.Close()

	// ReadAt should not affect the current position
	buf := make([]byte, 5)
	n, err := file.ReadAt(buf, 6)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "world", string(buf))

	// Read from current position (should still be at 0)
	buf2 := make([]byte, 5)
	n, err = file.Read(buf2)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", string(buf2))
}

func TestLocalFile_Truncate(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file
	file, err := fs.Create("truncate.txt")
	require.NoError(t, err)
	defer file.Close()

	_, err = file.Write([]byte("hello world"))
	require.NoError(t, err)

	// Truncate to 5 bytes
	err = file.Truncate(5)
	require.NoError(t, err)

	// Close and reopen to verify
	err = file.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(tmpDir, "truncate.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}

func TestLocalFile_WriteAtOffset(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file and write initial content
	file, err := fs.OpenFile("offset_write.txt", os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer file.Close()

	_, err = file.Write([]byte("hello world"))
	require.NoError(t, err)

	// Seek back and overwrite
	_, err = file.Seek(0, io.SeekStart)
	require.NoError(t, err)

	_, err = file.Write([]byte("HELLO"))
	require.NoError(t, err)

	err = file.Close()
	require.NoError(t, err)

	// Verify
	content, err := os.ReadFile(filepath.Join(tmpDir, "offset_write.txt"))
	require.NoError(t, err)
	assert.Equal(t, "HELLO world", string(content))
}

func TestLocalFile_Append(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create initial file
	err := os.WriteFile(filepath.Join(tmpDir, "append.txt"), []byte("hello"), 0o644)
	require.NoError(t, err)

	// Open in append mode
	file, err := fs.OpenFile("append.txt", os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	defer file.Close()

	_, err = file.Write([]byte(" world"))
	require.NoError(t, err)

	err = file.Close()
	require.NoError(t, err)

	// Verify
	content, err := os.ReadFile(filepath.Join(tmpDir, "append.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))
}

func TestLocalFile_LockUnlock(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	file, err := fs.Create("lock_test.txt")
	require.NoError(t, err)
	defer file.Close()

	// Lock should succeed
	err = file.Lock()
	require.NoError(t, err)

	// Unlock should succeed
	err = file.Unlock()
	require.NoError(t, err)
}

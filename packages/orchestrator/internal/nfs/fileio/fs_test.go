package fileio

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalFS_Create(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	file, err := fs.Create("test.txt")
	require.NoError(t, err)
	defer file.Close()

	n, err := file.Write([]byte("hello world"))
	require.NoError(t, err)
	assert.Equal(t, 11, n)

	err = file.Close()
	require.NoError(t, err)

	// Verify file exists and has correct content
	content, err := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))
}

func TestLocalFS_Open(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file first
	testContent := []byte("test content")
	err := os.WriteFile(filepath.Join(tmpDir, "existing.txt"), testContent, 0o644)
	require.NoError(t, err)

	// Open it via the filesystem
	file, err := fs.Open("existing.txt")
	require.NoError(t, err)
	defer file.Close()

	content, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)
}

func TestLocalFS_OpenFile_CreateExclusive(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file
	file, err := fs.OpenFile("exclusive.txt", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	require.NoError(t, err)
	file.Close()

	// Try to create it again with O_EXCL - should fail
	_, err = fs.OpenFile("exclusive.txt", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	assert.True(t, os.IsExist(err))
}

func TestLocalFS_Stat(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file
	testPath := filepath.Join(tmpDir, "stat_test.txt")
	err := os.WriteFile(testPath, []byte("content"), 0o644)
	require.NoError(t, err)

	info, err := fs.Stat("stat_test.txt")
	require.NoError(t, err)
	assert.Equal(t, "stat_test.txt", info.Name())
	assert.Equal(t, int64(7), info.Size())
	assert.False(t, info.IsDir())
}

func TestLocalFS_Rename(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file
	err := os.WriteFile(filepath.Join(tmpDir, "old.txt"), []byte("content"), 0o644)
	require.NoError(t, err)

	err = fs.Rename("old.txt", "new.txt")
	require.NoError(t, err)

	// Old file should not exist
	_, err = os.Stat(filepath.Join(tmpDir, "old.txt"))
	assert.True(t, os.IsNotExist(err))

	// New file should exist
	content, err := os.ReadFile(filepath.Join(tmpDir, "new.txt"))
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))
}

func TestLocalFS_Remove(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file
	testPath := filepath.Join(tmpDir, "to_remove.txt")
	err := os.WriteFile(testPath, []byte("content"), 0o644)
	require.NoError(t, err)

	err = fs.Remove("to_remove.txt")
	require.NoError(t, err)

	_, err = os.Stat(testPath)
	assert.True(t, os.IsNotExist(err))
}

func TestLocalFS_MkdirAll(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	err := fs.MkdirAll("a/b/c", 0o755)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(tmpDir, "a/b/c"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestLocalFS_ReadDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create some files
	err := os.WriteFile(filepath.Join(tmpDir, "file1.txt"), []byte("1"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "file2.txt"), []byte("2"), 0o644)
	require.NoError(t, err)
	err = os.Mkdir(filepath.Join(tmpDir, "subdir"), 0o755)
	require.NoError(t, err)

	entries, err := fs.ReadDir("")
	require.NoError(t, err)
	assert.Len(t, entries, 3)

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	assert.Contains(t, names, "file1.txt")
	assert.Contains(t, names, "file2.txt")
	assert.Contains(t, names, "subdir")
}

func TestLocalFS_Symlink(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file
	err := os.WriteFile(filepath.Join(tmpDir, "target.txt"), []byte("target content"), 0o644)
	require.NoError(t, err)

	// Create symlink
	err = fs.Symlink("target.txt", "link.txt")
	require.NoError(t, err)

	// Read the link
	target, err := fs.Readlink("link.txt")
	require.NoError(t, err)
	assert.Equal(t, "target.txt", target)

	// Verify we can read through the link
	file, err := fs.Open("link.txt")
	require.NoError(t, err)
	defer file.Close()

	content, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, "target content", string(content))
}

func TestLocalFS_Lstat(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a file and a symlink
	err := os.WriteFile(filepath.Join(tmpDir, "target.txt"), []byte("content"), 0o644)
	require.NoError(t, err)
	err = os.Symlink("target.txt", filepath.Join(tmpDir, "link.txt"))
	require.NoError(t, err)

	// Stat follows the link
	info, err := fs.Stat("link.txt")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0), info.Mode()&os.ModeSymlink)

	// Lstat returns info about the link itself
	info, err = fs.Lstat("link.txt")
	require.NoError(t, err)
	assert.NotEqual(t, os.FileMode(0), info.Mode()&os.ModeSymlink)
}

func TestLocalFS_Chroot(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	// Create a subdirectory with a file
	err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "subdir", "file.txt"), []byte("content"), 0o644)
	require.NoError(t, err)

	// Chroot into subdir
	subFS, err := fs.Chroot("subdir")
	require.NoError(t, err)

	// Access the file relative to the new root
	file, err := subFS.Open("file.txt")
	require.NoError(t, err)
	defer file.Close()

	content, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))
}

func TestLocalFS_TempFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)

	file, err := fs.TempFile("", "test-")
	require.NoError(t, err)
	defer file.Close()

	assert.Contains(t, file.Name(), "test-")
	assert.True(t, filepath.HasPrefix(file.Name(), tmpDir))
}

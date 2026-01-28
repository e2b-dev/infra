package fileio

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChange_Chmod(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)
	change := newChange(fs)

	// Create a file
	err := os.WriteFile(filepath.Join(tmpDir, "chmod_test.txt"), []byte("content"), 0o644)
	require.NoError(t, err)

	// Change permissions
	err = change.Chmod("chmod_test.txt", 0o755)
	require.NoError(t, err)

	// Verify
	info, err := os.Stat(filepath.Join(tmpDir, "chmod_test.txt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestChange_Chown(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)
	change := newChange(fs)

	// Create a file
	testPath := filepath.Join(tmpDir, "chown_test.txt")
	err := os.WriteFile(testPath, []byte("content"), 0o644)
	require.NoError(t, err)

	// Get current uid/gid
	info, err := os.Stat(testPath)
	require.NoError(t, err)

	stat := info.Sys().(*syscall.Stat_t)
	currentUID := int(stat.Uid)
	currentGID := int(stat.Gid)

	// Chown to same uid/gid (should always succeed without root)
	err = change.Chown("chown_test.txt", currentUID, currentGID)
	require.NoError(t, err)
}

func TestChange_Lchown(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)
	change := newChange(fs)

	// Create a file and a symlink
	targetPath := filepath.Join(tmpDir, "target.txt")
	err := os.WriteFile(targetPath, []byte("content"), 0o644)
	require.NoError(t, err)

	linkPath := filepath.Join(tmpDir, "link.txt")
	err = os.Symlink("target.txt", linkPath)
	require.NoError(t, err)

	// Get current uid/gid
	info, err := os.Lstat(linkPath)
	require.NoError(t, err)

	stat := info.Sys().(*syscall.Stat_t)
	currentUID := int(stat.Uid)
	currentGID := int(stat.Gid)

	// Lchown should change the link itself, not the target
	err = change.Lchown("link.txt", currentUID, currentGID)
	require.NoError(t, err)
}

func TestChange_Chtimes(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs := NewLocalFS(tmpDir)
	change := newChange(fs)

	// Create a file
	testPath := filepath.Join(tmpDir, "chtimes_test.txt")
	err := os.WriteFile(testPath, []byte("content"), 0o644)
	require.NoError(t, err)

	// Set specific times
	atime := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	mtime := time.Date(2021, 6, 15, 18, 30, 0, 0, time.UTC)

	err = change.Chtimes("chtimes_test.txt", atime, mtime)
	require.NoError(t, err)

	// Verify mtime (atime may not be reliable on all filesystems)
	info, err := os.Stat(testPath)
	require.NoError(t, err)
	assert.Equal(t, mtime, info.ModTime().UTC())
}

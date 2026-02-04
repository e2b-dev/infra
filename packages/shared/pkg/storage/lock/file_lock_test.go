package lock

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryAcquireLock_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "test-resource-1")

	// Create the directory for the lock
	err := os.MkdirAll(testPath, 0o755)
	require.NoError(t, err)

	file, err := TryAcquireLock(ctx, testPath)

	require.NoError(t, err)
	assert.NotNil(t, file)

	// Verify lock file exists
	lockPath := getLockFilePath(testPath)
	_, err = os.Stat(lockPath)
	require.NoError(t, err)

	// Clean up
	err = ReleaseLock(ctx, file)
	require.NoError(t, err)

	// Verify lock file was removed
	_, err = os.Stat(lockPath)
	assert.True(t, os.IsNotExist(err))
}

func TestTryAcquireLock_AlreadyHeld(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "test-resource-2")

	// Create the directory for the lock
	err := os.MkdirAll(testPath, 0o755)
	require.NoError(t, err)

	// First acquisition should succeed
	file1, err1 := TryAcquireLock(ctx, testPath)
	require.NoError(t, err1)
	assert.NotNil(t, file1)
	defer ReleaseLock(ctx, file1)

	// Second acquisition should fail (lock already held)
	file2, err2 := TryAcquireLock(ctx, testPath)
	require.ErrorIs(t, err2, ErrLockAlreadyHeld)
	assert.Nil(t, file2)
}

func TestTryAcquireLock_StaleLock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "test-resource-3")

	// Create lock directory
	err := os.MkdirAll(testPath, 0o755)
	require.NoError(t, err)

	// Create a stale lock file
	lockPath := getLockFilePath(testPath)
	staleLockFile, err := os.Create(lockPath)
	require.NoError(t, err)
	err = staleLockFile.Close()
	require.NoError(t, err)

	// Set modification time to past (older than TTL)
	pastTime := time.Now().Add(-2 * defaultLockTTL)
	err = os.Chtimes(lockPath, pastTime, pastTime)
	require.NoError(t, err)

	// Try to acquire lock - should succeed after cleaning stale lock
	file, err := TryAcquireLock(ctx, testPath)
	require.NoError(t, err)
	assert.NotNil(t, file)

	// Clean up
	err = ReleaseLock(ctx, file)
	require.NoError(t, err)
}

func TestReleaseLock_NilFile(t *testing.T) {
	t.Parallel()
	// Should not panic or error when releasing nil file
	err := ReleaseLock(t.Context(), nil)
	require.NoError(t, err)
}

func TestGetLockFilePath_Consistency(t *testing.T) {
	t.Parallel()
	testPath := "/tmp/test-key"

	// Same path should always produce the same lock file path
	path1 := getLockFilePath(testPath)
	path2 := getLockFilePath(testPath)

	assert.Equal(t, path1, path2)
	assert.Contains(t, path1, testPath)
	assert.Contains(t, path1, ".lock")
}

func TestGetLockFilePath_DifferentPaths(t *testing.T) {
	t.Parallel()
	path1 := "/tmp/resource-1"
	path2 := "/tmp/resource-2"

	// Different paths should produce different lock file paths
	lockPath1 := getLockFilePath(path1)
	lockPath2 := getLockFilePath(path2)

	assert.NotEqual(t, lockPath1, lockPath2)
}

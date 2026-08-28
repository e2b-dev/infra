//go:build linux

package build

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLocalDiffFileCloseToDiffRemovesPartialOnError verifies CloseToDiff does not
// leave the partial cache file behind when it fails to produce a usable Diff.
// Nothing registers such a file in the DiffStore, so a leaked orphan would sit in
// the cache dir unreclaimable by disk-pressure eviction until process restart.
func TestLocalDiffFileCloseToDiffRemovesPartialOnError(t *testing.T) {
	t.Parallel()

	f, err := NewLocalDiffFile(t.TempDir(), "build-test-id", Rootfs)
	require.NoError(t, err)

	// Non-empty so we're past the zero-size NoDiff branch and into materialization.
	_, err = f.File.WriteAt(make([]byte, 128), 0)
	require.NoError(t, err)

	cachePath := f.cachePath
	require.FileExists(t, cachePath)

	// Force a materialization failure: closing the fd makes the Sync inside
	// CloseToDiff fail, driving the error path.
	require.NoError(t, f.File.Close())

	diff, err := f.CloseToDiff(blockSize)
	require.Error(t, err)
	require.Nil(t, diff)
	require.NoFileExists(t, cachePath, "partial diff file must be removed on materialization failure")
}

// TestLocalDiffFileCloseToDiffRemovesEmptyCacheFile verifies the zero-size NoDiff
// branch removes the cache file: NoDiff owns no path, so nothing downstream can.
func TestLocalDiffFileCloseToDiffRemovesEmptyCacheFile(t *testing.T) {
	t.Parallel()

	f, err := NewLocalDiffFile(t.TempDir(), "build-test-id", Rootfs)
	require.NoError(t, err)

	cachePath := f.cachePath
	require.FileExists(t, cachePath)

	// Nothing written, so CloseToDiff takes the zero-size NoDiff branch.
	diff, err := f.CloseToDiff(blockSize)
	require.NoError(t, err)
	require.IsType(t, &NoDiff{}, diff)
	require.NoFileExists(t, cachePath, "empty diff cache file must be removed")
}

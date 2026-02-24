package filesystem

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPathOnNetworkMount(t *testing.T) {
	t.Parallel()

	// Test with a regular directory (should not be on network mount)
	tempDir := t.TempDir()
	isNetwork, err := IsPathOnNetworkMount(tempDir)
	require.NoError(t, err)
	assert.False(t, isNetwork, "temp directory should not be on a network mount")
}

func TestIsPathOnNetworkMount_FuseMount(t *testing.T) {
	t.Parallel()

	// Require bindfs to be available
	_, err := exec.LookPath("bindfs")
	require.NoError(t, err, "bindfs must be installed for this test")

	// Require fusermount to be available (needed for unmounting)
	_, err = exec.LookPath("fusermount")
	require.NoError(t, err, "fusermount must be installed for this test")

	// Create source and mount directories
	sourceDir := t.TempDir()
	mountDir := t.TempDir()

	// Mount sourceDir onto mountDir using bindfs (FUSE)
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "bindfs", sourceDir, mountDir)
	require.NoError(t, cmd.Run(), "failed to mount bindfs")

	// Ensure we unmount on cleanup
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "fusermount", "-u", mountDir).Run()
	})

	// Test that the FUSE mount is detected
	isNetwork, err := IsPathOnNetworkMount(mountDir)
	require.NoError(t, err)
	assert.True(t, isNetwork, "FUSE mount should be detected as network filesystem")

	// Test that the source directory is NOT detected as network mount
	isNetworkSource, err := IsPathOnNetworkMount(sourceDir)
	require.NoError(t, err)
	assert.False(t, isNetworkSource, "source directory should not be detected as network filesystem")
}

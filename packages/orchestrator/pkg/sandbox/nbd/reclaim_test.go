//go:build linux

package nbd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsDeviceConnectedIn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nbd0"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nbd1"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nbd0", "pid"), []byte("123"), 0o600))

	connected, err := isDeviceConnectedIn(dir, 0)
	require.NoError(t, err)
	require.True(t, connected)

	connected, err = isDeviceConnectedIn(dir, 1)
	require.NoError(t, err)
	require.False(t, connected)
}

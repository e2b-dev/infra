package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReclaimSandboxFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cacheDir := t.TempDir()
	matching := []string{
		filepath.Join(tmpDir, "fc-sbx-rand.sock"),
		filepath.Join(tmpDir, "uffd-sbx-rand.sock"),
		filepath.Join(tmpDir, "fc-metrics-sbx-rand.fifo"),
		filepath.Join(cacheDir, "rootfs-sbx-rand.cow"),
		filepath.Join(cacheDir, "rootfs-sbx-rand.link"),
	}
	for _, path := range matching {
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	}
	decoys := []string{
		filepath.Join(tmpDir, "fc.sock"),
		filepath.Join(tmpDir, "fc-sbx.sock"),
		filepath.Join(tmpDir, "uffd-sbx.sock"),
		filepath.Join(tmpDir, "fc-metrics-sbx.fifo"),
		filepath.Join(cacheDir, "rootfs-sbx.cow"),
		filepath.Join(cacheDir, "rootfs-sbx.link"),
	}
	for _, path := range decoys {
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	}

	reclaimed, failures := ReclaimSandboxFiles(tmpDir, cacheDir)
	require.Empty(t, failures)
	require.Equal(t, len(matching), reclaimed)

	for _, path := range matching {
		require.NoFileExists(t, path)
	}
	for _, path := range decoys {
		require.FileExists(t, path)
	}
}

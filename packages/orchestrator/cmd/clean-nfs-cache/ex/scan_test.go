package ex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/stretchr/testify/require"
)

func TestScanDir(t *testing.T) {
	path := t.TempDir()
	CreateTestDir(path, 157, 10000, 1000)
	t.Cleanup(func() {
		os.RemoveAll(path)
	})

	c := NewCleaner(Options{
		Path: path,
	}, logger.NewNopLogger())
	df, err := os.Open(path)
	require.NoError(t, err)
	defer df.Close()

	dir := c.cacheRoot
	err = c.scanDir(df, &dir, true, false)
	require.NoError(t, err)
	require.True(t, dir.IsScanned())
	require.False(t, dir.IsEmpty())
	require.False(t, dir.AreFilesScanned())
	require.NotEmpty(t, dir.Dirs)

	sub := dir.Dirs[0]
	dfsub, err := os.Open(filepath.Join(path, sub.Name))
	require.NoError(t, err)
	defer dfsub.Close()

	err = c.scanDir(dfsub, &sub, true, false)
	require.NoError(t, err)
	require.True(t, sub.IsScanned())
	require.False(t, sub.IsEmpty())
	require.False(t, sub.AreFilesScanned())
	require.NotEmpty(t, sub.Files)

	err = c.scanDir(dfsub, &sub, false, true)
	require.NoError(t, err)
	require.True(t, sub.IsScanned())
	require.False(t, sub.IsEmpty())
	require.True(t, sub.AreFilesScanned())
	require.NotEmpty(t, sub.Files)
}

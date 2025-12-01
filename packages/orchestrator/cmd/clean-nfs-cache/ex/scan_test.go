package ex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClean(t *testing.T) {
	testFileSize := 10000
	path := t.TempDir()
	CreateTestDir(path, 1, 10, testFileSize)
	t.Cleanup(func() {
		os.RemoveAll(path)
	})
	ctx := context.Background()

	c := NewCleaner(Options{
		Path:    path,
		DeleteN: 1,
		BatchN:  5,
		DryRun:  false,
	})

	err := c.Clean(ctx, 1, uint64(2*testFileSize-1))
	require.NoError(t, err)
	require.Equal(t, 2, int(c.RemoveC.Load()))
	require.Equal(t, 2*testFileSize, int(c.DeletedBytes))
	require.Equal(t, 2, len(c.DeletedAges))

	entries, err := os.ReadDir(path)
	require.NoError(t, err)
	require.Equal(t, 1, len(entries))

	entries, err = os.ReadDir(filepath.Join(path, entries[0].Name()))
	require.NoError(t, err)
	require.Equal(t, 8, len(entries))
}

func TestScanDir(t *testing.T) {
	path := t.TempDir()
	CreateTestDir(path, 157, 10000, 1000)
	t.Cleanup(func() {
		os.RemoveAll(path)
	})

	c := NewCleaner(Options{
		Path: path,
	})
	df, err := os.Open(path)
	require.NoError(t, err)
	defer df.Close()

	dir := c.root
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

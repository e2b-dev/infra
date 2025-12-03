package ex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/stretchr/testify/require"
)

func TestClean(t *testing.T) {
	const (
		testFileSize = 7317
		NDirs        = 100
		NFiles       = 10000
		PercentClean = 13
	)

	ctx := context.Background()

	for _, nScan := range []int{1, 2, 4, 16, 64} {
		for _, nDel := range []int{1, 2, 4, 8} {
			t.Run(fmt.Sprintf("S%v-D%v", nScan, nDel), func(t *testing.T) {
				path := t.TempDir()
				CreateTestDir(path, NDirs, NFiles, testFileSize)
				t.Cleanup(func() {
					os.RemoveAll(path)
				})
				start := time.Now()
				targetBytesToDelete := uint64(NFiles*testFileSize*PercentClean/100) + 1
				c := NewCleaner(Options{
					Path:                path,
					DeleteN:             NFiles / 100,
					BatchN:              NFiles / 10,
					DryRun:              false,
					NumScanners:         nScan,
					NumDeleters:         nDel,
					TargetBytesToDelete: targetBytesToDelete,
				}, logger.NewNopLogger())

				err := c.Clean(ctx)
				require.NoError(t, err)
				require.GreaterOrEqual(t, c.DeletedBytes, targetBytesToDelete)
				t.Logf("Cleaned %d out of %d bytes in %v with S%d D%d", c.DeletedBytes, targetBytesToDelete, time.Since(start), nScan, nDel)
			})
		}
	}
}

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

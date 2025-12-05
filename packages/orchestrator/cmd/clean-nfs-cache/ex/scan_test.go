package ex

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

	wg := &sync.WaitGroup{}
	wg.Add(1)
	c.statRequestCh = make(chan *statReq, 1)
	quitCh := make(chan struct{})
	go c.Statter(context.Background(), quitCh, wg)
	defer func() {
		close(quitCh)
		wg.Wait()
	}()

	df, err := os.Open(path)
	require.NoError(t, err)
	defer df.Close()

	dir, err := c.scanDir([]*Dir{c.root})
	require.NoError(t, err)
	require.True(t, dir.IsScanned())
	require.False(t, dir.IsEmpty())
	require.NotEmpty(t, dir.Dirs)

	sub := dir.Dirs[0]
	dfsub, err := os.Open(filepath.Join(path, sub.Name))
	require.NoError(t, err)
	defer dfsub.Close()

	sub, err = c.scanDir([]*Dir{c.root, sub})
	require.NoError(t, err)
	require.True(t, sub.IsScanned())
	require.False(t, sub.IsEmpty())
	require.NotEmpty(t, sub.Files)
}

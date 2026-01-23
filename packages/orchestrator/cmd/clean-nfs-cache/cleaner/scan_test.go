package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestScanDir(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	CreateTestDir(path, 157, 10000, 1000)
	t.Cleanup(func() {
		os.RemoveAll(path)
	})

	c := NewCleaner(Options{
		Path: path,
	}, logger.NewNopLogger())

	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(1)
	c.statRequestCh = make(chan *statReq, 1)
	go c.Statter(ctx, wg)
	defer func() {
		cancel()
		wg.Wait()
	}()

	df, err := os.Open(path)
	require.NoError(t, err)
	defer df.Close()

	dir, err := c.scanDir(ctx, []*Dir{c.root})
	require.NoError(t, err)
	require.True(t, dir.IsScanned())
	require.False(t, dir.IsEmpty())
	require.NotEmpty(t, dir.Dirs)

	sub := dir.Dirs[0]
	dfsub, err := os.Open(filepath.Join(path, sub.Name))
	require.NoError(t, err)
	defer dfsub.Close()

	sub, err = c.scanDir(ctx, []*Dir{c.root, sub})
	require.NoError(t, err)
	require.True(t, sub.IsScanned())
	require.False(t, sub.IsEmpty())
	require.NotEmpty(t, sub.Files)
}

func TestRandomSubdirOrOldestFile(t *testing.T) {
	t.Parallel()
	// build a Dir with files sorted so that the oldest file is at the end
	d := &Dir{}
	count := 10
	d.Files = make([]File, count)
	for i := range count {
		// timestamps decrease so the last entry is the oldest
		ts := int64(1000 - i)
		name := "f" + strconv.Itoa(i)
		d.Files[i] = File{
			Name:      name,
			ATimeUnix: ts,
			Size:      uint64(100000 + i),
		}
	}
	d.sort()

	f, sub, err := d.randomSubdirOrOldestFile()
	require.NoError(t, err)
	require.Nil(t, sub)
	require.NotNil(t, f)
	require.Equal(t, "f9", f.Name)
	require.Equal(t, int64(991), f.ATimeUnix)

	// build a dir with no files, so we get a subdir for sure
	d2 := &Dir{}
	count = 5
	d2.Dirs = make([]*Dir, count)
	for i := range count {
		name := "d" + strconv.Itoa(i)
		d2.Dirs[i] = NewDir(name)
	}
	d2.sort()
	f, sub, err = d2.randomSubdirOrOldestFile()
	require.NoError(t, err)
	require.Nil(t, f)
	require.NotNil(t, sub)
	require.Contains(t, map[string]bool{
		"d0": true,
		"d1": true,
		"d2": true,
		"d3": true,
		"d4": true,
	}, sub.Name)

	// build an empty dir
	d3 := &Dir{}
	_, _, err = d3.randomSubdirOrOldestFile()
	require.ErrorIs(t, err, ErrNoFiles)
}

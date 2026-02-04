package cleaner

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestDirSort(t *testing.T) {
	t.Parallel()
	d := &Dir{
		Name: "testdir",
		Dirs: []*Dir{
			{Name: "subdirB"},
			{Name: "subdirA"},
			{Name: "subdirC"},
		},
		Files: []File{
			{Name: "file3.txt", ATimeUnix: 300},
			{Name: "file1.txt", ATimeUnix: 100},
			{Name: "file2.txt", ATimeUnix: 200},
		},
	}

	d.sort()

	require.Equal(t, "subdirA", d.Dirs[0].Name)
	require.Equal(t, "subdirB", d.Dirs[1].Name)
	require.Equal(t, "subdirC", d.Dirs[2].Name)

	require.Equal(t, "file3.txt", d.Files[0].Name)
	require.Equal(t, int64(300), d.Files[0].ATimeUnix)
	require.Equal(t, "file2.txt", d.Files[1].Name)
	require.Equal(t, int64(200), d.Files[1].ATimeUnix)
	require.Equal(t, "file1.txt", d.Files[2].Name)
	require.Equal(t, int64(100), d.Files[2].ATimeUnix)
}

func TestCleanDeletesOldestFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	defer os.RemoveAll(root)

	// create root path used by Cleaner
	rootPath := filepath.Join(root, "root")
	err := os.MkdirAll(rootPath, 0o755)
	require.NoError(t, err)

	subdirs := []string{"subA", "subB"}
	origFiles := map[string][]string{}

	now := time.Now()

	// Create 2 subdirs each with 9 files: file0 (newest) ... file8 (oldest)
	for _, sd := range subdirs {
		dirPath := filepath.Join(rootPath, sd)
		err = os.MkdirAll(dirPath, 0o755)
		require.NoError(t, err)

		names := []string{}
		for i := range 9 {
			name := filepath.Join(dirPath, "file"+strconv.Itoa(i)+".txt")
			names = append(names, filepath.Base(name))
			// write 512 bytes to ensure non-zero size
			err = os.WriteFile(name, make([]byte, 512), 0o644)
			require.NoError(t, err)

			// file0 should be newest
			ageMinutes := time.Duration(10*i) * time.Minute // ensure clear ordering
			mtime := now.Add(-ageMinutes)
			err = os.Chtimes(name, mtime, mtime)
			require.NoError(t, err)
		}
		origFiles[sd] = names
	}

	// Configure Cleaner to delete 2 files (target bytes equal to 2 files)
	opts := Options{
		Path:                rootPath,
		BatchN:              4,
		DeleteN:             2,
		TargetBytesToDelete: 1024, // 2 * 512
		DryRun:              false,
		MaxConcurrentStat:   1,
		MaxConcurrentScan:   1,
		MaxConcurrentDelete: 1,
	}

	c := NewCleaner(opts, logger.NewNopLogger())

	err = c.Clean(t.Context())
	require.NoError(t, err)

	// Collect which files remain and which were deleted
	deleted := []string{}
	for _, sd := range subdirs {
		dirPath := filepath.Join(rootPath, sd)
		entries, err := os.ReadDir(dirPath)
		require.NoError(t, err)
		remaining := map[string]bool{}
		for _, e := range entries {
			if !e.IsDir() {
				remaining[e.Name()] = true
			}
		}
		for _, fn := range origFiles[sd] {
			if !remaining[fn] {
				deleted = append(deleted, filepath.Join(sd, fn))
			}
		}
	}

	// Expect at least 2 deletions, it can be more due to concurrency.
	require.GreaterOrEqual(t, len(deleted), 2)

	// The two files must have been some combination of 7 and 8 (from whichever
	// folder) Because of concurrency, sometimes we may pick an extra batch of
	// candidates, so include 6 as well.
	for _, d := range deleted {
		require.Regexp(t, `.+/file(6|7|8)\.txt`, d)
	}
}

func TestSplitBatch(t *testing.T) {
	t.Parallel()

	c := &Cleaner{
		Options: Options{DeleteN: 3},
	}

	batch := []*Candidate{
		{FullPath: "file0.txt", ATimeUnix: 100},
		{FullPath: "file1.txt", ATimeUnix: 200},
		{FullPath: "file2.txt", ATimeUnix: 300},
		{FullPath: "file3.txt", ATimeUnix: 400},
		{FullPath: "file4.txt", ATimeUnix: 500},
	}

	toDelete, toReinsert := c.splitBatch(batch)

	require.Len(t, toDelete, 3)
	require.Equal(t, "file0.txt", toDelete[0].FullPath)
	require.Equal(t, "file1.txt", toDelete[1].FullPath)
	require.Equal(t, "file2.txt", toDelete[2].FullPath)

	require.Len(t, toReinsert, 2)
	require.Equal(t, "file3.txt", toReinsert[0].FullPath)
	require.Equal(t, "file4.txt", toReinsert[1].FullPath)
}

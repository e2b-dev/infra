package ex

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestDirSort(t *testing.T) {
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

func TestCleanDeletesTwoFiles(t *testing.T) {
	root := t.TempDir()
	defer os.RemoveAll(root)

	// create root path used by Cleaner
	rootPath := filepath.Join(root, "root")
	err := os.MkdirAll(rootPath, 0o755)
	require.NoError(t, err)

	subdirs := []string{"subA", "subB"}
	origFiles := map[string][]string{}

	now := time.Now()

	// Create 2 subdirs each with 9 files: file0 (oldest) ... file8 (newest)
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

			// file0 should be oldest, file8 newest
			ageMinutes := 100*(5*i) + i // ensure clear ordering
			mtime := now.Add(time.Duration(-ageMinutes) * time.Minute)
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

	// Run Clean with a timeout to avoid hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = c.Clean(ctx)
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

	// Expect at least 2 deletions
	require.GreaterOrEqual(t, len(deleted), 2)

	// Expect that the newest files remain
	for _, sd := range subdirs {
		expectedFile := filepath.Join(sd, "file8.txt")
		require.NotContains(t, deleted, expectedFile, "expected newest files to remain")
	}
}

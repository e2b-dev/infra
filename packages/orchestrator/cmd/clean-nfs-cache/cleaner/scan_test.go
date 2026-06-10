package cleaner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
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
	}, logger.NewNopLogger(), nil)

	ctx, cancel := context.WithCancel(t.Context())
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

// runOrphanScan runs scanDir against {root}/{buildID}/ with one stat worker.
// Returns the ErrNoFiles wrap (if any) so callers can assert on the
// orphan-vs-empty path.
func runOrphanScan(t *testing.T, c *Cleaner, buildID string) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	wg := &sync.WaitGroup{}
	wg.Add(1)
	c.statRequestCh = make(chan *statReq, 1)
	go c.Statter(ctx, wg)
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	buildDir := NewDir(buildID)
	c.root.Dirs = append(c.root.Dirs, buildDir)

	_, err := c.scanDir(ctx, []*Dir{c.root, buildDir})

	return err
}

func TestScanDir_OrphanReap(t *testing.T) {
	t.Parallel()

	t.Run("reaps BuildID dir with no memfile or rootfs subdir past grace", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		buildID := "orphan-build"
		buildDir := filepath.Join(root, buildID)
		require.NoError(t, os.MkdirAll(filepath.Join(buildDir, "stale-junk"), 0o755))
		// Backdate the BuildID dir mtime so it is past the grace period.
		old := time.Now().Add(-2 * time.Hour)
		require.NoError(t, os.Chtimes(buildDir, old, old))

		c := NewCleaner(Options{
			Path: root,
		}, logger.NewNopLogger(), nil)

		err := runOrphanScan(t, c, buildID)
		require.ErrorIs(t, err, ErrNoFiles)
		_, statErr := os.Stat(buildDir)
		require.True(t, os.IsNotExist(statErr), "BuildID dir should have been removed, got: %v", statErr)
	})

	t.Run("skips BuildID dir within grace period", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		buildID := "fresh-build"
		buildDir := filepath.Join(root, buildID)
		// Fresh dir with no memfile/rootfs subdir but mtime = now → within grace.
		require.NoError(t, os.MkdirAll(filepath.Join(buildDir, "cache", "sandbox-uuid"), 0o755))

		c := NewCleaner(Options{
			Path: root,
		}, logger.NewNopLogger(), nil)

		err := runOrphanScan(t, c, buildID)
		// Fresh orphan-shaped dir is left alone; the scan should NOT delete it.
		// It may legitimately return ErrNoFiles for "no eligible files to evict
		// in this dir" — but the dir itself must remain.
		if err != nil {
			require.True(t, errors.Is(err, ErrNoFiles), "unexpected error: %v", err)
		}
		_, statErr := os.Stat(buildDir)
		require.NoError(t, statErr, "fresh BuildID dir must not be reaped")
	})

	t.Run("does not reap BuildID dir that has memfile subdir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		buildID := "live-build"
		buildDir := filepath.Join(root, buildID)
		require.NoError(t, os.MkdirAll(filepath.Join(buildDir, storage.MemfileName), 0o755))
		// Backdate to past grace so the only reason not to reap is the data subdir.
		old := time.Now().Add(-2 * time.Hour)
		require.NoError(t, os.Chtimes(buildDir, old, old))

		c := NewCleaner(Options{
			Path: root,
		}, logger.NewNopLogger(), nil)

		_ = runOrphanScan(t, c, buildID)
		_, statErr := os.Stat(buildDir)
		require.NoError(t, statErr, "BuildID with memfile/ subdir must not be reaped")
	})

	t.Run("does not reap BuildID dir that has rootfs subdir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		buildID := "live-build-rootfs"
		buildDir := filepath.Join(root, buildID)
		require.NoError(t, os.MkdirAll(filepath.Join(buildDir, storage.RootfsName), 0o755))
		old := time.Now().Add(-2 * time.Hour)
		require.NoError(t, os.Chtimes(buildDir, old, old))

		c := NewCleaner(Options{
			Path: root,
		}, logger.NewNopLogger(), nil)

		_ = runOrphanScan(t, c, buildID)
		_, statErr := os.Stat(buildDir)
		require.NoError(t, statErr, "BuildID with rootfs.ext4/ subdir must not be reaped")
	})

	t.Run("respects DryRun", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		buildID := "dryrun-build"
		buildDir := filepath.Join(root, buildID)
		require.NoError(t, os.MkdirAll(filepath.Join(buildDir, "stale-junk"), 0o755))
		old := time.Now().Add(-2 * time.Hour)
		require.NoError(t, os.Chtimes(buildDir, old, old))

		c := NewCleaner(Options{
			Path:   root,
			DryRun: true,
		}, logger.NewNopLogger(), nil)

		err := runOrphanScan(t, c, buildID)
		require.ErrorIs(t, err, ErrNoFiles)
		_, statErr := os.Stat(buildDir)
		require.NoError(t, statErr, "DryRun must not actually delete the dir")
	})
}

func TestVerifyChunksCacheRoot(t *testing.T) {
	t.Parallel()

	t.Run("accepts root with a UUID dir that has memfile subdir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		buildID := uuid.NewString()
		require.NoError(t, os.MkdirAll(filepath.Join(root, buildID, storage.MemfileName), 0o755))
		require.NoError(t, VerifyChunksCacheRoot(root))
	})

	t.Run("accepts root with a UUID dir that has rootfs subdir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		buildID := uuid.NewString()
		require.NoError(t, os.MkdirAll(filepath.Join(root, buildID, storage.RootfsName), 0o755))
		require.NoError(t, VerifyChunksCacheRoot(root))
	})

	t.Run("accepts empty root", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, VerifyChunksCacheRoot(root))
	})

	t.Run("accepts root with no UUID-named children", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		// e.g. a fresh mount with only the lost+found / .snapshot kind of stuff,
		// or some non-cache scratch dir. No UUID-shaped entries → permissive.
		require.NoError(t, os.MkdirAll(filepath.Join(root, "lost+found"), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(root, "some-non-uuid-thing"), 0o755))
		require.NoError(t, VerifyChunksCacheRoot(root))
	})

	t.Run("rejects root whose UUID children lack memfile/rootfs subdirs", func(t *testing.T) {
		t.Parallel()
		// Simulates pointing the cleaner one level too high: the children
		// here parse as UUIDs but contain unrelated stuff.
		root := t.TempDir()
		for range 3 {
			require.NoError(t, os.MkdirAll(filepath.Join(root, uuid.NewString(), "stale-junk"), 0o755))
		}
		err := VerifyChunksCacheRoot(root)
		require.Error(t, err)
		require.Contains(t, err.Error(), "refusing")
	})

	t.Run("rejects nonexistent path", func(t *testing.T) {
		t.Parallel()
		err := VerifyChunksCacheRoot(filepath.Join(t.TempDir(), "does-not-exist"))
		require.Error(t, err)
	})
}

package cleaner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// newTestCleaner builds a cleaner over root with a large byte target so every
// deletion candidate is considered.
func newTestCleaner(root string, mutate func(o *Options)) *Cleaner {
	o := Options{
		Path:                root,
		TargetBytesToDelete: 1 << 50,
		SampleMinFiles:      8,
		SamplePercent:       10,
		SampleMaxFiles:      64,
		Grace:               0, // tests: no create-time grace, builds eligible immediately
		MaxConcurrentScan:   4,
		MaxConcurrentStat:   4,
		MaxConcurrentDelete: 4,
	}
	if mutate != nil {
		mutate(&o)
	}

	return NewCleaner(o, logger.NewNopLogger(), nil)
}

// writeChunk creates {root}/{build}/{dataDir}/{NNN-SIZE.bin} with the given
// size and atime, making parent dirs as needed.
func writeChunk(t *testing.T, root, build, dataDir string, idx int, size int, atime time.Time) {
	t.Helper()
	dir := filepath.Join(root, build, dataDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	name := fmt.Sprintf("%012d-%d.bin", idx, size)
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, make([]byte, size), 0o644))
	require.NoError(t, os.Chtimes(p, atime, atime))
}

// TestScanBuild_CompressionAware guards the bug the redesign fixes: a compressed
// build (memfile.zstd/ + rootfs.ext4.zstd/ holding .frm chunks) must be
// recognized as a live build and sized from its chunks — not misread as
// chunkless and reaped with size 0.
func TestScanBuild_CompressionAware(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, dir := range []string{storage.MemfileName + ".zstd", storage.RootfsName + ".zstd"} {
		d := filepath.Join(root, "compressed", dir)
		require.NoError(t, os.MkdirAll(d, 0o755))
		// .frm chunk: hex on-disk size field (0x100000 = 1 MiB).
		p := filepath.Join(d, "0000000000000000-100000.frm")
		require.NoError(t, os.WriteFile(p, make([]byte, 0x100000), 0o644))
		require.NoError(t, os.Chtimes(p, old, old))
	}

	c := newTestCleaner(root, nil)
	require.NoError(t, c.Clean(t.Context()))

	require.NoDirExists(t, filepath.Join(root, "compressed"))
	require.Equal(t, int64(1), c.Deleted.Load())
	// Sized from the two .frm chunks (1 MiB each) + the flat other-files charge —
	// proves the chunks were counted, not skipped as a chunkless build.
	require.Equal(t, uint64(2<<20+otherFilesBytesEstimate), c.BytesFreed.Load())
	// readdirs = 1 root + the 2 data dirs, and no per-build readdir (data dirs are
	// opened by name). Guards against reintroducing a build-dir readdir.
	require.Equal(t, int64(3), c.ReadDirC.Load())
}

// TestGraceProtectsFreshBuilds checks the create-time filter (build-dir btime)
// skips a freshly-created build before it can be deleted — whether it already has
// chunks or is still in progress with an empty data dir — even with a huge target.
func TestGraceProtectsFreshBuilds(t *testing.T) {
	t.Parallel()

	t.Run("fresh build with chunks", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeChunk(t, root, "warm", storage.MemfileName, 0, 4096, time.Now())

		c := newTestCleaner(root, func(o *Options) { o.Grace = time.Hour })
		require.NoError(t, c.Clean(t.Context()))
		require.DirExists(t, filepath.Join(root, "warm"))
		require.Equal(t, int64(0), c.Deleted.Load())
	})

	t.Run("in-progress build with an empty data dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		// Data dir created, no chunk written yet — the fresh btime filters it out.
		require.NoError(t, os.MkdirAll(filepath.Join(root, "inprogress", storage.MemfileName+".zstd"), 0o755))

		c := newTestCleaner(root, func(o *Options) { o.Grace = time.Hour })
		require.NoError(t, c.Clean(t.Context()))
		require.DirExists(t, filepath.Join(root, "inprogress"))
		require.Equal(t, int64(0), c.Deleted.Load())
	})
}

// TestScanBuild_EmptyDirReaped checks that a build dir with no chunk data sorts
// coldest (warmest 0) and is reaped first — and that dry-run leaves it alone.
func TestScanBuild_EmptyDirReaped(t *testing.T) {
	t.Parallel()

	t.Run("reaps a leftover dir with no data", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "leftover", "stale-junk"), 0o755))

		require.NoError(t, newTestCleaner(root, nil).Clean(t.Context()))
		require.NoDirExists(t, filepath.Join(root, "leftover"))
	})

	t.Run("dry run does not reap", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "leftover", "stale-junk"), 0o755))

		require.NoError(t, newTestCleaner(root, func(o *Options) { o.DryRun = true }).Clean(t.Context()))
		require.DirExists(t, filepath.Join(root, "leftover"))
	})
}

// TestBuildSampleCapsScan checks that only BuildSample-many builds are scanned
// when the cap is set, regardless of how many the root holds.
func TestBuildSampleCapsScan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for i := range 10 {
		writeChunk(t, root, fmt.Sprintf("build-%02d", i), storage.MemfileName, 0, 4096, time.Now())
	}

	// clamp(min=3, 10%·10=1, max=3) = 3 of the 10 builds.
	c := newTestCleaner(root, func(o *Options) {
		o.DryRun = true
		o.BuildSampleMin = 3
		o.BuildSamplePercent = 10
		o.BuildSampleMax = 3
	})
	require.NoError(t, c.Clean(t.Context()))
	require.Equal(t, int64(3), c.BuildsScanned.Load(), "only the build-sample cap should be scanned")
}

func TestClean_SamplesAreCapped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// One build, 500 chunks. K = clamp(8, ⌈10%·500⌉=50, 64) = 50.
	for i := range 500 {
		writeChunk(t, root, "one", storage.MemfileName, i, 4096, time.Now())
	}

	c := newTestCleaner(root, func(o *Options) { o.DryRun = true })
	require.NoError(t, c.Clean(t.Context()))
	require.Equal(t, int64(50), c.StatxC.Load(), "should statx clamp(8, 10%%·500, 64)=50, not all 500")
}

func TestVerifyChunksCacheRoot(t *testing.T) {
	t.Parallel()

	t.Run("accepts root with a UUID dir that has memfile subdir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, uuid.NewString(), storage.MemfileName), 0o755))
		require.NoError(t, VerifyChunksCacheRoot(root))
	})

	t.Run("accepts root with a compressed data dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, uuid.NewString(), storage.RootfsName+".zstd"), 0o755))
		require.NoError(t, VerifyChunksCacheRoot(root))
	})

	t.Run("accepts empty root", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, VerifyChunksCacheRoot(t.TempDir()))
	})

	t.Run("accepts root with no UUID-named children", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "lost+found"), 0o755))
		require.NoError(t, VerifyChunksCacheRoot(root))
	})

	t.Run("rejects root whose UUID children lack data subdirs", func(t *testing.T) {
		t.Parallel()
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
		require.Error(t, VerifyChunksCacheRoot(filepath.Join(t.TempDir(), "nope")))
	})
}

package cleaner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestRunBench_EndToEnd_SingleScale(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	opts := BenchOptions{
		Path:           root,
		NumBuildIDs:    4,
		ChunksPerBuild: 3,
		FileSize:       128,
		Concurrency:    2,
	}

	require.NoError(t, RunBench(t.Context(), opts, nil, logger.NewNopLogger()))

	// Artifacts should have been removed by default.
	_, statErr := os.Stat(filepath.Join(root, "bench-shard-read"))
	require.True(t, os.IsNotExist(statErr), "bench artifacts should be removed; got: %v", statErr)
}

func TestRunBench_EndToEnd_Sweep(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	opts := BenchOptions{
		Path:           root,
		Scales:         []int{2, 4, 6},
		ChunksPerBuild: 2,
		FileSize:       128,
		Concurrency:    2,
		KeepArtifacts:  true,
	}

	require.NoError(t, RunBench(t.Context(), opts, nil, logger.NewNopLogger()))

	// max(Scales) controls how much data exists on disk; smaller scales run
	// against a prefix of the BuildID list and share that tree.
	flatRoot := filepath.Join(root, "bench-shard-read", "flat")
	flatBuildDirs, err := os.ReadDir(flatRoot)
	require.NoError(t, err)
	require.Len(t, flatBuildDirs, 6, "setup should create max(Scales) BuildIDs")
}

func TestRunBench_KeepArtifactsCreatesBothLayouts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	opts := BenchOptions{
		Path:           root,
		NumBuildIDs:    2,
		ChunksPerBuild: 2,
		FileSize:       64,
		Concurrency:    1,
		KeepArtifacts:  true,
	}

	require.NoError(t, RunBench(t.Context(), opts, nil, logger.NewNopLogger()))

	flatRoot := filepath.Join(root, "bench-shard-read", "flat")
	shardedRoot := filepath.Join(root, "bench-shard-read", "sharded")

	// Each flat/{BuildID}/memfile/ dir exists with the right file count.
	flatBuildDirs, err := os.ReadDir(flatRoot)
	require.NoError(t, err)
	require.Len(t, flatBuildDirs, opts.NumBuildIDs)

	// Sharded root has 2-char hex shard dirs.
	shardEntries, err := os.ReadDir(shardedRoot)
	require.NoError(t, err)
	require.NotEmpty(t, shardEntries)
	for _, e := range shardEntries {
		require.True(t, e.IsDir())
		require.Len(t, e.Name(), 2, "first shard segment should be 2 hex chars; got %q", e.Name())
	}

	// Pick one BuildID, confirm both layouts have the same file count.
	flatBuildID := flatBuildDirs[0].Name()
	_, parseErr := uuid.Parse(flatBuildID)
	require.NoError(t, parseErr, "BuildID dirs should be UUID-named")

	flatChunks, err := os.ReadDir(filepath.Join(flatRoot, flatBuildID, "memfile"))
	require.NoError(t, err)
	require.Len(t, flatChunks, opts.ChunksPerBuild)

	shardedChunks, err := os.ReadDir(filepath.Join(shardedRoot, flatBuildID[:2], flatBuildID[2:4], flatBuildID, "memfile"))
	require.NoError(t, err)
	require.Len(t, shardedChunks, opts.ChunksPerBuild)

	// Chunk filenames match the {index:012d}-{size}.bin convention.
	for _, c := range flatChunks {
		require.True(t, strings.HasSuffix(c.Name(), ".bin"), "unexpected chunk name: %q", c.Name())
	}
}

func TestRunBench_ValidatesOptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts BenchOptions
		want string
	}{
		{"missing path", BenchOptions{NumBuildIDs: 1, ChunksPerBuild: 1, FileSize: 1, Concurrency: 1}, "Path is required"},
		{"no scales no build ids", BenchOptions{Path: "/tmp", ChunksPerBuild: 1, FileSize: 1, Concurrency: 1}, "Scales or NumBuildIDs"},
		{"zero chunks", BenchOptions{Path: "/tmp", NumBuildIDs: 1, FileSize: 1, Concurrency: 1}, "ChunksPerBuild"},
		{"zero concurrency", BenchOptions{Path: "/tmp", NumBuildIDs: 1, ChunksPerBuild: 1, FileSize: 1}, "Concurrency"},
		{"negative scale entry", BenchOptions{Path: "/tmp", Scales: []int{-1}, ChunksPerBuild: 1, FileSize: 1, Concurrency: 1}, "Scales entry"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RunBench(t.Context(), tc.opts, nil, logger.NewNopLogger())
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestShardPathFor(t *testing.T) {
	t.Parallel()
	require.Equal(t, "ab/cd", shardPathFor("abcd1234-5678-90ab-cdef-112233445566"))
	require.Equal(t, "00/00", shardPathFor("0000abcd-0000-0000-0000-000000000000"))
	// Defensive: very short strings just round-trip.
	require.Equal(t, "abc", shardPathFor("abc"))
}

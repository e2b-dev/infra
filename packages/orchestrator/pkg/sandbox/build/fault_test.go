//go:build linux

package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

const readSegmentsFaultChildEnv = "BUILD_FAULT_TEST_CHILD"

// TestReadSegments_MmapFault reproduces the production crash where an
// unrecoverable disk read error under a build cache file raised SIGBUS inside
// the readSegments memmove (readSegment → localDiff.ReadAt → Cache.ReadAt →
// copy) and killed the whole orchestrator. The fault must instead surface as
// an error from readSegments, failing only the one read.
//
// Runs in a subprocess because without the guard the fault is a fatal runtime
// error that would take the whole test binary down.
func TestReadSegments_MmapFault(t *testing.T) {
	if os.Getenv(readSegmentsFaultChildEnv) == "1" {
		readSegmentsFaultChild(t)

		return
	}
	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=^TestReadSegments_MmapFault$", "-test.v")
	cmd.Env = append(os.Environ(), readSegmentsFaultChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err,
		"child crashed: a memory fault in readSegments must become an error, not kill the process\n%s", out)
}

func readSegmentsFaultChild(t *testing.T) {
	t.Helper()

	const (
		blockSize = int64(4096)
		size      = 4 * blockSize
	)

	path := filepath.Join(t.TempDir(), "cache")
	diff, err := newLocalDiff(GetDiffStoreKey("build-id", Memfile), path, size, blockSize)
	require.NoError(t, err)
	defer diff.Close()

	// All mapped pages now sit beyond EOF: accessing them raises SIGBUS,
	// like paging in an unreadable disk block.
	require.NoError(t, os.Truncate(path, 0))

	f := &File{}
	buf := make([]byte, size)
	segments := []readSegment{
		{dstOff: 0, srcOff: 0, length: 2 * blockSize, diff: diff},
		{dstOff: int(2 * blockSize), srcOff: 2 * blockSize, length: 2 * blockSize, diff: diff},
	}

	// Parallel branch (errgroup) — the exact path from the crash stacks.
	err = f.readSegments(t.Context(), buf, segments, 4)
	require.ErrorIs(t, err, block.ErrMemoryFault)

	// Sequential branch.
	err = f.readSegments(t.Context(), buf, segments, 1)
	require.ErrorIs(t, err, block.ErrMemoryFault)
}

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

// TestReadSegments_MmapFault reproduces the production crash where a bad
// sector under a build cache file raised SIGBUS inside the readSegments
// memmove and killed the whole orchestrator; the fault must surface as an
// error instead. Runs in a subprocess because an unguarded fault is a fatal
// runtime error.
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

	// Mapped pages beyond EOF fault with SIGBUS on access.
	require.NoError(t, os.Truncate(path, 0))

	f := &File{}
	buf := make([]byte, size)
	segments := []readSegment{
		{dstOff: 0, srcOff: 0, length: 2 * blockSize, diff: diff},
		{dstOff: int(2 * blockSize), srcOff: 2 * blockSize, length: 2 * blockSize, diff: diff},
	}

	// Parallel (errgroup) branch — the path from the crash stacks.
	err = f.readSegments(t.Context(), buf, segments, 4)
	require.ErrorIs(t, err, block.ErrMemoryFault)

	// Sequential branch.
	err = f.readSegments(t.Context(), buf, segments, 1)
	require.ErrorIs(t, err, block.ErrMemoryFault)
}

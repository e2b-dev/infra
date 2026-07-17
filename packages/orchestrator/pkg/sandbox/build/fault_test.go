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

// Reproduces the production crash where a bad sector under a build cache
// raised SIGBUS in readSegments and killed the orchestrator. Runs in a
// subprocess: an unguarded fault would kill the test binary.
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

	// Mapped pages beyond EOF raise SIGBUS on access, like a bad sector.
	require.NoError(t, os.Truncate(path, 0))

	f := &File{}
	buf := make([]byte, size)
	segments := []readSegment{
		{dstOff: 0, srcOff: 0, length: 2 * blockSize, diff: diff},
		{dstOff: int(2 * blockSize), srcOff: 2 * blockSize, length: 2 * blockSize, diff: diff},
	}

	// Both the parallel and the sequential branch.
	var faultErr *block.MemoryFaultError
	err = f.readSegments(t.Context(), buf, segments, 4)
	require.ErrorAs(t, err, &faultErr)

	err = f.readSegments(t.Context(), buf, segments, 1)
	require.ErrorAs(t, err, &faultErr)
}

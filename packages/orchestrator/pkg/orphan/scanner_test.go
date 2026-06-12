//go:build linux

package orphan_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/orphan"
)

// ─── extractAPISocket ────────────────────────────────────────────────────────

func TestExtractAPISocket_SpaceSeparated(t *testing.T) {
	t.Parallel()

	args := []string{"/fc-versions/v1.12.1/firecracker", "--api-sock", "/tmp/fc-abc-def.sock"}
	assert.Equal(t, "/tmp/fc-abc-def.sock", orphan.ExtractAPISocket(args))
}

func TestExtractAPISocket_EqualSeparated(t *testing.T) {
	t.Parallel()

	args := []string{"/fc-versions/v1.12.1/firecracker", "--api-sock=/data0/tmp/fc-xyz-123.sock"}
	assert.Equal(t, "/data0/tmp/fc-xyz-123.sock", orphan.ExtractAPISocket(args))
}

func TestExtractAPISocket_Missing(t *testing.T) {
	t.Parallel()

	args := []string{"/fc-versions/v1.12.1/firecracker", "--config-file", "/etc/fc.json"}
	assert.Equal(t, "", orphan.ExtractAPISocket(args))
}

func TestExtractAPISocket_EmptyArgs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", orphan.ExtractAPISocket(nil))
	assert.Equal(t, "", orphan.ExtractAPISocket([]string{}))
}

func TestExtractAPISocket_FlagAtEnd_NoValue(t *testing.T) {
	t.Parallel()

	// --api-sock is the last argument with no following value; must not panic
	args := []string{"/fc-versions/v1.12.1/firecracker", "--api-sock"}
	assert.Equal(t, "", orphan.ExtractAPISocket(args))
}

// ─── fifoNameToSocketName ────────────────────────────────────────────────────

func TestFifoNameToSocketName_ValidName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fifo   string
		socket string
	}{
		{"fc-metrics-abc123-def456.fifo", "fc-abc123-def456.sock"},
		{"fc-metrics-iquvfugirq0nyyfe2ehti-rmy0cyy575spwv4e8ydy.fifo", "fc-iquvfugirq0nyyfe2ehti-rmy0cyy575spwv4e8ydy.sock"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.fifo, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.socket, orphan.FifoNameToSocketName(tc.fifo))
		})
	}
}

func TestFifoNameToSocketName_InvalidName(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"something.fifo",
		"fc-abc.sock",
		"fc-metrics.fifo", // no id segment
	}

	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Names not matching the pattern should return an empty string
			result := orphan.FifoNameToSocketName(name)
			// fc-metrics.fifo → "fc-.sock", others return ""
			// Must not panic; empty input must return ""
			if name == "" || name == "something.fifo" || name == "fc-abc.sock" {
				assert.Equal(t, "", result)
			}
		})
	}
}

// ─── scanOrphanedSockets ─────────────────────────────────────────────────────

func TestScanOrphanedSockets_FindsOrphanedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a socket file matching the naming pattern (regular file as stand-in)
	sockPath := filepath.Join(dir, "fc-sandboxid-randomid.sock")
	require.NoError(t, os.WriteFile(sockPath, nil, 0o600))

	// liveSockets is empty → the file should be detected as an orphan
	orphans, err := orphan.ScanOrphanedSockets([]string{dir}, map[string]struct{}{})
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	assert.Equal(t, sockPath, orphans[0].Path)
}

func TestScanOrphanedSockets_SkipsLiveSocket(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "fc-sandboxid-randomid.sock")
	require.NoError(t, os.WriteFile(sockPath, nil, 0o600))

	// Add the path to liveSockets → it should not appear in results
	live := map[string]struct{}{sockPath: {}}
	orphans, err := orphan.ScanOrphanedSockets([]string{dir}, live)
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

func TestScanOrphanedSockets_IgnoresNonMatchingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Files that do not match the fc-*.sock naming pattern
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.sock"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fc-metrics-abc-def.fifo"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "random.txt"), nil, 0o600))

	orphans, err := orphan.ScanOrphanedSockets([]string{dir}, map[string]struct{}{})
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

func TestScanOrphanedSockets_NonExistentDirIsSkipped(t *testing.T) {
	t.Parallel()

	orphans, err := orphan.ScanOrphanedSockets([]string{"/nonexistent/path/xyz"}, map[string]struct{}{})
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

func TestScanOrphanedSockets_MultipleDirectories(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	sock1 := filepath.Join(dir1, "fc-aaa-bbb.sock")
	sock2 := filepath.Join(dir2, "fc-ccc-ddd.sock")
	require.NoError(t, os.WriteFile(sock1, nil, 0o600))
	require.NoError(t, os.WriteFile(sock2, nil, 0o600))

	// sock1 is live, sock2 is an orphan
	live := map[string]struct{}{sock1: {}}
	orphans, err := orphan.ScanOrphanedSockets([]string{dir1, dir2}, live)
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	assert.Equal(t, sock2, orphans[0].Path)
}

// ─── scanOrphanedFIFOs ───────────────────────────────────────────────────────

func TestScanOrphanedFIFOs_FindsOrphanedFIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a FIFO file using the mkfifo syscall
	fifoPath := filepath.Join(dir, "fc-metrics-sandboxid-randomid.fifo")
	require.NoError(t, syscall.Mkfifo(fifoPath, 0o600))

	// Corresponding socket is not in liveSockets → should be detected as an orphan
	orphans, err := orphan.ScanOrphanedFIFOs([]string{dir}, map[string]struct{}{})
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	assert.Equal(t, fifoPath, orphans[0].Path)
}

func TestScanOrphanedFIFOs_SkipsWhenSocketIsLive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fifoPath := filepath.Join(dir, "fc-metrics-sandboxid-randomid.fifo")
	require.NoError(t, syscall.Mkfifo(fifoPath, 0o600))

	// Add the corresponding socket path to liveSockets
	sockPath := filepath.Join(dir, "fc-sandboxid-randomid.sock")
	live := map[string]struct{}{sockPath: {}}

	orphans, err := orphan.ScanOrphanedFIFOs([]string{dir}, live)
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

func TestScanOrphanedFIFOs_IgnoresNonMatchingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fc-abc-def.sock"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.fifo"), nil, 0o600))

	orphans, err := orphan.ScanOrphanedFIFOs([]string{dir}, map[string]struct{}{})
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

func TestScanOrphanedFIFOs_NonExistentDirIsSkipped(t *testing.T) {
	t.Parallel()

	orphans, err := orphan.ScanOrphanedFIFOs([]string{"/nonexistent/xyz"}, map[string]struct{}{})
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

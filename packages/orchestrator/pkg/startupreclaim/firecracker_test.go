//go:build linux

package startupreclaim

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoverFirecrackerPIDs(t *testing.T) {
	t.Parallel()

	procDir := t.TempDir()
	createProc(t, procDir, 101, []string{"/fc-versions/v1.7/firecracker", "--api-sock", "/tmp/fc.sock"})
	createProc(t, procDir, 100, []string{"bash", "-c", "ip netns exec ns-7 /opt/firecracker"})
	createProc(t, procDir, 200, []string{"bash", "-c", "not firecracker"})
	// Processes whose basename merely contains "firecracker" must not match, so
	// helpers like a monitor are never killed.
	createProc(t, procDir, 300, []string{"/usr/bin/firecracker-monitor", "--watch"})
	createProc(t, procDir, 400, []string{"/opt/firecrackerd"})

	pids, err := discoverFirecrackerPIDs(procDir)
	require.NoError(t, err)
	require.Equal(t, []int{101}, pids)
}

func TestDiscoverFirecrackerPIDsMissingProcDir(t *testing.T) {
	t.Parallel()

	_, err := discoverFirecrackerPIDs(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
}

func createProc(t *testing.T, procDir string, pid int, cmdline []string) {
	t.Helper()

	pidDir := filepath.Join(procDir, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(pidDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte(strings.Join(cmdline, "\x00")+"\x00"), 0o600))
}

//go:build linux
// +build linux

package inspector

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcTrackerMembershipDelta uses a synthetic "cgroup" — really
// just a directory containing a cgroup.procs file we control — to
// exercise the membership-delta path without needing a real cgroup
// hierarchy or root privileges.
func TestProcTrackerMembershipDelta(t *testing.T) {
	dir := t.TempDir()
	procsPath := filepath.Join(dir, "cgroup.procs")
	require.NoError(t, os.WriteFile(procsPath, []byte("1\n2\n3\n"), 0o644))

	tr := newProcTracker([]string{dir}, 0)

	_, _ = tr.Reset()

	// No change yet → query reports unchanged.
	changed, _ := tr.Query()
	assert.False(t, changed, "no membership change since reset")

	// Pretend a new pid joined.
	require.NoError(t, os.WriteFile(procsPath, []byte("1\n2\n3\n4\n"), 0o644))
	changed, _ = tr.Query()
	assert.True(t, changed, "membership added: should report changed")

	// Reset re-baselines; now equal again.
	_, _ = tr.Reset()
	changed, _ = tr.Query()
	assert.False(t, changed, "after reset, baseline matches; no change")

	// Pretend a pid exited.
	require.NoError(t, os.WriteFile(procsPath, []byte("1\n2\n"), 0o644))
	changed, _ = tr.Query()
	assert.True(t, changed, "membership shrunk: should report changed")
}

// TestProcTrackerExcludesSelf verifies the daemon's own PID isn't
// counted, which is critical because envd holds many always-dirty
// pages.
func TestProcTrackerExcludesSelf(t *testing.T) {
	dir := t.TempDir()
	procsPath := filepath.Join(dir, "cgroup.procs")
	self := os.Getpid()
	require.NoError(t, os.WriteFile(procsPath, []byte(fmt.Sprintf("%d\n", self)), 0o644))

	tr := newProcTracker([]string{dir}, self)
	n, _ := tr.Reset()
	assert.Equal(t, 0, n, "self PID must be excluded from baseline")
}

// TestProcTrackerSoftDirtyOnRealChild verifies the soft-dirty path on
// the running kernel. Skipped if the kernel doesn't support it (older
// or hardened kernels return EINVAL on clear_refs=4).
func TestProcTrackerSoftDirtyOnRealChild(t *testing.T) {
	if !probeSoftDirty() {
		t.Skip("kernel lacks CONFIG_MEM_SOFT_DIRTY")
	}

	// Spawn a long-lived child that allocates memory after we begin
	// tracking. Using `sh -c 'sleep 0.05; head -c 1M /dev/urandom >/dev/null; sleep 5'`
	// gives us a PID that survives reset and dirties pages on read+discard.
	cmd := exec.Command("sh", "-c", `head -c 524288 /dev/urandom >/dev/null; sleep 5`)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Hand-rolled "cgroup" directory containing only this child.
	dir := t.TempDir()
	procsPath := filepath.Join(dir, "cgroup.procs")
	require.NoError(t, os.WriteFile(procsPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644))

	tr := newProcTracker([]string{dir}, 0)
	_, ok := tr.Reset()
	if !ok {
		t.Skip("soft-dirty reset returned ok=false (likely permission)")
	}

	// Wait long enough for the child to write its 512 KiB.
	time.Sleep(150 * time.Millisecond)

	changed, ok := tr.Query()
	require.True(t, ok, "tracker must remain healthy")
	assert.True(t, changed, "child wrote pages after reset; soft-dirty should fire")
}

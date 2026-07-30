//go:build linux

package rootfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNBDDevicePath pins the device allowlist the jailed debugfs is constrained
// to: only a bare NBD node, so a bad caller can't point the rewrite at an
// arbitrary block device (a partition, a real disk, or a traversal).
func TestNBDDevicePath(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"/dev/nbd0", "/dev/nbd1", "/dev/nbd42"} {
		assert.Truef(t, nbdDevicePath.MatchString(ok), "%q should be accepted", ok)
	}
	for _, bad := range []string{
		"/dev/sda", "/dev/nbd", "/dev/nbd0p1", "/dev/nbd0 ", "/dev/nbdx",
		"../../dev/nbd0", "/dev/nbd0;rm", "/tmp/nbd0", "",
	} {
		assert.Falsef(t, nbdDevicePath.MatchString(bad), "%q should be rejected", bad)
	}
}

// TestRunDebugfsRejectsNonNBDDevice verifies the guard is actually wired into the
// jail launcher (not just the regex): a non-NBD device is refused before any
// debugfs/systemd-run process is spawned, so this needs no root or debugfs.
func TestRunDebugfsRejectsNonNBDDevice(t *testing.T) {
	t.Parallel()

	out, err := runDebugfs(t.Context(), "/dev/sda", t.TempDir(), "swap", "rm /usr/bin/envd\n", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to run debugfs on unexpected device path")
	assert.Empty(t, out)
}

// TestEnvdExecutable pins the mode check the swap/rollback verify relies on: a
// content match is not enough — a silent `sif` failure could leave envd
// non-executable, which this must catch from debugfs `stat` output.
func TestEnvdExecutable(t *testing.T) {
	t.Parallel()

	exec := "Inode: 12   Type: regular    Mode:  0755   Flags: 0x0\nUser: 0   Group: 0   Size: 100\n"
	nonExec := "Inode: 12   Type: regular    Mode:  0644   Flags: 0x0\nUser: 0   Group: 0   Size: 100\n"
	dir := "Inode: 2   Type: directory    Mode:  0755   Flags: 0x0\n"

	assert.True(t, envdExecutable(exec), "regular file 0755 is executable")
	assert.True(t, envdExecutable("Type: regular    Mode:  0555"), "0555 has the owner-exec bit")
	assert.False(t, envdExecutable(nonExec), "regular file 0644 is NOT executable (silent sif failure)")
	assert.False(t, envdExecutable(dir), "a directory is not an executable regular file")
	assert.False(t, envdExecutable(""), "empty stat output is not executable")
	assert.False(t, envdExecutable("Type: regular"), "missing Mode is not executable")
}

// TestCappedBuffer verifies debugfs output is bounded: writes past the limit are
// discarded, but every Write still reports a full write so the child is never
// blocked or handed a short-write error.
func TestCappedBuffer(t *testing.T) {
	t.Parallel()

	b := &cappedBuffer{limit: 4}

	n, err := b.Write([]byte("abcdef"))
	require.NoError(t, err)
	assert.Equal(t, 6, n, "Write must report the full length even when capped")
	assert.Equal(t, "abcd", string(b.Bytes()))

	n, err = b.Write([]byte("ghij"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "abcd", string(b.Bytes()), "buffer stays capped across writes")
}

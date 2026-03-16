package chrooted

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMountNS_Basic(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("skipping test because it requires root privileges")
	}

	// Pin the test goroutine to a single OS thread so that all namespace
	// checks observe the same thread's mount namespace.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Verify thread isolation and namespace switching
	// We'll check the mount namespace ID
	origNSID := getMountNSID(t)

	ns, err := tempMountNS(t.Context())
	require.NoError(t, err)
	defer ns.Close()

	afterTempNSID := getMountNSID(t)

	// Verify we can run something in the namespace
	err = ns.Do(func() error {
		// Just a simple check that we are running
		return nil
	})
	require.NoError(t, err)

	var innerNSID uint64
	err = ns.Do(func() error {
		innerNSID = getMountNSID(t)
		assert.NotEqual(t, origNSID, innerNSID, "Should be in a different mount namespace")

		return nil
	})
	require.NoError(t, err)

	outerNSIDAfterRun := getMountNSID(t)
	assert.NotEqual(t, innerNSID, outerNSIDAfterRun, "Should not be using the isolated mount namespace")
	assert.NotEqual(t, innerNSID, afterTempNSID, "Should not be using the temporary mount namespace")
}

func TestMountNS_Close(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("skipping test because it requires root privileges")
	}

	ns, err := tempMountNS(t.Context())
	require.NoError(t, err)

	err = ns.Close()
	require.NoError(t, err)

	// Double close should return error
	err = ns.Close()
	require.ErrorIs(t, err, ErrNamespaceClosed)

	// Do on closed namespace should return error
	err = ns.Do(func() error { return nil })
	require.ErrorIs(t, err, ErrNamespaceClosed)

	// Set on closed namespace should return error
	err = ns.Set()
	require.ErrorIs(t, err, ErrNamespaceClosed)
}

func TestMountNS_ErrorPropagation(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("skipping test because it requires root privileges")
	}

	ns, err := tempMountNS(t.Context())
	require.NoError(t, err)
	defer ns.Close()

	expectedErr := os.ErrPermission
	err = ns.Do(func() error {
		return expectedErr
	})
	assert.ErrorIs(t, err, expectedErr)
}

func TestIsNSorErr(t *testing.T) {
	t.Parallel()

	// Should work for /proc/self/ns/mnt
	err := IsNSorErr("/proc/self/ns/mnt")
	require.NoError(t, err)

	// Should fail for a regular file
	tmpFile, err := os.CreateTemp(t.TempDir(), "not-a-ns")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	err = IsNSorErr(tmpFile.Name())
	require.ErrorAs(t, err, &NSPathNotNSError{})

	// Should fail for non-existent path
	err = IsNSorErr("/non/existent/path/that/really/should/not/exist")
	require.ErrorAs(t, err, &NSPathNotExistError{})
}

func getMountNSID(t *testing.T) uint64 {
	t.Helper()

	// Use the thread-specific path so we read the calling thread's mount
	// namespace, not the thread-group leader's.
	var stat unix.Stat_t
	err := unix.Stat(getCurrentThreadMountNSPath(), &stat)
	require.NoError(t, err)

	return stat.Ino
}

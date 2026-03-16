package chrooted

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMountNS_Basic(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("skipping test because it requires root privileges")
	}

	// Verify thread isolation and namespace switching
	// We'll check the mount namespace ID
	origNSID := getMountNSID(t)

	println("original:", getCurrentThreadMountNSPath(), "=", getMountNSID(t))

	ns, err := tempMountNS(t.Context())
	require.NoError(t, err)
	defer ns.Close()

	afterTempNSID := getMountNSID(t)
	println("after mount namespace:", getCurrentThreadMountNSPath(), "=", afterTempNSID)

	// Verify we can run something in the namespace
	err = ns.Do(func() error {
		// Just a simple check that we are running
		return nil
	})
	assert.NoError(t, err)

	var innerNSID uint64
	err = ns.Do(func() error {
		println("inside mount namespace:", getCurrentThreadMountNSPath(), "=", getMountNSID(t))

		innerNSID = getMountNSID(t)
		assert.NotEqual(t, origNSID, innerNSID, "Should be in a different mount namespace")
		return nil
	})
	assert.NoError(t, err)

	outerNSIDAfterRun := getMountNSID(t)
	println("after doing:", getCurrentThreadMountNSPath(), "=", outerNSIDAfterRun)
	assert.NotEqual(t, innerNSID, outerNSIDAfterRun, "Should not be using the isolated mount namespace")
	assert.NotEqual(t, innerNSID, afterTempNSID, "Should not be using the temporary mount namespace")
}

func TestMountNS_Close(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("skipping test because it requires root privileges")
	}

	ns, err := tempMountNS(t.Context())
	require.NoError(t, err)

	err = ns.Close()
	assert.NoError(t, err)

	// Double close should return error
	err = ns.Close()
	assert.ErrorIs(t, err, ErrNamespaceClosed)

	// Do on closed namespace should return error
	err = ns.Do(func() error { return nil })
	assert.ErrorIs(t, err, ErrNamespaceClosed)

	// Set on closed namespace should return error
	err = ns.Set()
	assert.ErrorIs(t, err, ErrNamespaceClosed)
}

func TestMountNS_ErrorPropagation(t *testing.T) {
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
	// Should work for /proc/self/ns/mnt
	err := IsNSorErr("/proc/self/ns/mnt")
	assert.NoError(t, err)

	// Should fail for a regular file
	tmpFile, err := os.CreateTemp("", "not-a-ns")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	err = IsNSorErr(tmpFile.Name())
	assert.ErrorAs(t, err, &NSPathNotNSError{})

	// Should fail for non-existent path
	err = IsNSorErr("/non/existent/path/that/really/should/not/exist")
	assert.ErrorAs(t, err, &NSPathNotExistError{})
}

func getMountNSID(t *testing.T) uint64 {
	t.Helper()
	// Using Stat_t.Ino for the inode number of the mount namespace file
	var stat unix.Stat_t
	err := unix.Stat("/proc/self/ns/mnt", &stat)
	require.NoError(t, err)
	return stat.Ino
}

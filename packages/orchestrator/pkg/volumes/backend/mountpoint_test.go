//go:build linux

package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFakeMounts writes a fake /proc/mounts-style file and overrides
// procMountsPath for the duration of the test.
func writeFakeMounts(t *testing.T, entries ...string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "proc-mounts-*")
	require.NoError(t, err)
	for _, e := range entries {
		_, err = fmt.Fprintln(f, e)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())

	old := procMountsPath
	procMountsPath = f.Name()
	t.Cleanup(func() { procMountsPath = old })
}

// fakeMountEntry builds a /proc/mounts line for the given path and fstype.
func fakeMountEntry(path, fstype string) string {
	return fmt.Sprintf("none %s %s rw,relatime 0 0", path, fstype)
}

// ── MountpointBackend ────────────────────────────────────────────────────────

func TestMountpointBackend_Type(t *testing.T) {
	t.Parallel()
	b := NewMountpointBackend(t.TempDir(), "testfs")
	assert.Equal(t, "testfs", b.Type())
}

func TestMountpointBackend_RootPath_Format(t *testing.T) {
	t.Parallel()

	root := "/mnt/myfs"
	teamID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	volumeID := uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")

	b := NewMountpointBackend(root, "testfs")
	want := fmt.Sprintf("%s/team-%s/vol-%s", root, teamID, volumeID)
	assert.Equal(t, want, b.RootPath(teamID, volumeID))
}

func TestMountpointBackend_CreateVolume_CreatesDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewMountpointBackend(root, "testfs")

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	assert.DirExists(t, b.RootPath(teamID, volumeID))
}

func TestMountpointBackend_CreateVolume_Idempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewMountpointBackend(root, "testfs")

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
}

func TestMountpointBackend_DeleteVolume_RemovesDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewMountpointBackend(root, "testfs")

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))

	// Write a file inside to confirm recursive removal.
	err := os.WriteFile(filepath.Join(b.RootPath(teamID, volumeID), "file"), []byte("x"), 0o600)
	require.NoError(t, err)

	require.NoError(t, b.DeleteVolume(context.Background(), teamID, volumeID))
	assert.NoDirExists(t, b.RootPath(teamID, volumeID))
}

func TestMountpointBackend_DeleteVolume_NonExistentDir_NoError(t *testing.T) {
	t.Parallel()

	b := NewMountpointBackend(t.TempDir(), "testfs")
	require.NoError(t, b.DeleteVolume(context.Background(), uuid.New(), uuid.New()))
}

func TestMountpointBackend_Healthy_RootMissing(t *testing.T) {
	// Not parallel: overrides procMountsPath global.
	writeFakeMounts(t) // empty — no entries

	b := NewMountpointBackend("/nonexistent/mountpoint", "testfs")
	err := b.Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

func TestMountpointBackend_Healthy_NotMounted(t *testing.T) {
	// Not parallel: overrides procMountsPath global.
	root := t.TempDir()
	writeFakeMounts(t) // empty — path not in mounts

	b := NewMountpointBackend(root, "testfs")
	err := b.Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mount point")
}

func TestMountpointBackend_Healthy_Mounted(t *testing.T) {
	// Not parallel: overrides procMountsPath global.
	root := t.TempDir()
	writeFakeMounts(t, fakeMountEntry(root, "fuse.testfs"))

	b := NewMountpointBackend(root, "testfs")
	require.NoError(t, b.Healthy(context.Background()))
}

// ── isMountPoint ─────────────────────────────────────────────────────────────

func TestIsMountPoint_Found(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t,
		"sysfs /sys sysfs rw 0 0",
		fakeMountEntry(root, "fuse.juicefs"),
		"tmpfs /tmp tmpfs rw 0 0",
	)

	ok, err := isMountPoint(root)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestIsMountPoint_NotFound(t *testing.T) {
	writeFakeMounts(t, "sysfs /sys sysfs rw 0 0")

	ok, err := isMountPoint("/mnt/something")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestIsMountPoint_EmptyFile(t *testing.T) {
	writeFakeMounts(t) // no entries

	ok, err := isMountPoint("/mnt/anything")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestIsMountPoint_PartialPrefixNotMatched(t *testing.T) {
	// /mnt/juicefs should not match /mnt/juice (prefix must be exact).
	writeFakeMounts(t, "none /mnt/juice fuse.juicefs rw 0 0")

	ok, err := isMountPoint("/mnt/juicefs")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ── mountFSType ──────────────────────────────────────────────────────────────

func TestMountFSType_Found(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t, fakeMountEntry(root, "fuse.juicefs"))

	fsType, err := mountFSType(root)
	require.NoError(t, err)
	assert.Equal(t, "fuse.juicefs", fsType)
}

func TestMountFSType_NotFound(t *testing.T) {
	writeFakeMounts(t, "sysfs /sys sysfs rw 0 0")

	fsType, err := mountFSType("/mnt/missing")
	require.NoError(t, err)
	assert.Equal(t, "", fsType)
}

func TestMountFSType_EmptyFile(t *testing.T) {
	writeFakeMounts(t)

	fsType, err := mountFSType("/mnt/anything")
	require.NoError(t, err)
	assert.Equal(t, "", fsType)
}

func TestMountFSType_MultipleEntries_ReturnsFirst(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t,
		"tmpfs /tmp tmpfs rw 0 0",
		fakeMountEntry(root, "fuse.juicefs"),
		fakeMountEntry(root, "tmpfs"), // duplicate — should never happen in practice
	)

	fsType, err := mountFSType(root)
	require.NoError(t, err)
	assert.Equal(t, "fuse.juicefs", fsType)
}

//go:build linux

package backend

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// healthyMount sets up procMountsPath so that root appears mounted with fstype,
// and creates the root directory so os.Stat succeeds.
func healthyMount(t *testing.T, root, fstype string) {
	t.Helper()
	writeFakeMounts(t, fakeMountEntry(root, fstype))
}

// ── JuiceFS ──────────────────────────────────────────────────────────────────

func TestJuiceFSBackend_Type(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "juicefs", NewJuiceFSBackend(t.TempDir()).Type())
}

func TestJuiceFSBackend_RootPath(t *testing.T) {
	t.Parallel()
	teamID, volumeID := uuid.New(), uuid.New()
	root := "/mnt/juicefs"
	b := NewJuiceFSBackend(root)
	assert.Equal(t, volumePath(root, teamID, volumeID), b.RootPath(teamID, volumeID))
}

func TestJuiceFSBackend_CreateDelete_RoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewJuiceFSBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	assert.DirExists(t, b.RootPath(teamID, volumeID))

	require.NoError(t, b.DeleteVolume(context.Background(), teamID, volumeID))
	assert.NoDirExists(t, b.RootPath(teamID, volumeID))
}

func TestJuiceFSBackend_Healthy_CorrectFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "fuse.juicefs")

	require.NoError(t, NewJuiceFSBackend(root).Healthy(context.Background()))
}

func TestJuiceFSBackend_Healthy_WrongFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "tmpfs")

	err := NewJuiceFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fuse.juicefs")
}

func TestJuiceFSBackend_Healthy_NotMounted(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t) // root not in mounts table

	err := NewJuiceFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mount point")
}

func TestJuiceFSBackend_Healthy_RootMissing(t *testing.T) {
	writeFakeMounts(t)

	err := NewJuiceFSBackend("/nonexistent/juicefs").Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

// ── CephFS ───────────────────────────────────────────────────────────────────

func TestCephFSBackend_Type(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "cephfs", NewCephFSBackend(t.TempDir()).Type())
}

func TestCephFSBackend_Healthy_KernelClient(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "ceph")

	require.NoError(t, NewCephFSBackend(root).Healthy(context.Background()))
}

func TestCephFSBackend_Healthy_FUSEClient(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "fuse.ceph-fuse")

	require.NoError(t, NewCephFSBackend(root).Healthy(context.Background()))
}

func TestCephFSBackend_Healthy_CephPrefixVariant(t *testing.T) {
	// Any fstype with "ceph" prefix (e.g. ceph-fuse in older kernels) is accepted.
	root := t.TempDir()
	healthyMount(t, root, "ceph-fuse")

	require.NoError(t, NewCephFSBackend(root).Healthy(context.Background()))
}

func TestCephFSBackend_Healthy_WrongFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "ext4")

	err := NewCephFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ceph")
}

func TestCephFSBackend_Healthy_NotMounted(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t)

	err := NewCephFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mount point")
}

// ── GlusterFS ────────────────────────────────────────────────────────────────

func TestGlusterFSBackend_Type(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "glusterfs", NewGlusterFSBackend(t.TempDir()).Type())
}

func TestGlusterFSBackend_Healthy_CorrectFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "fuse.glusterfs")

	require.NoError(t, NewGlusterFSBackend(root).Healthy(context.Background()))
}

func TestGlusterFSBackend_Healthy_WrongFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "nfs")

	err := NewGlusterFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fuse.glusterfs")
}

func TestGlusterFSBackend_Healthy_NotMounted(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t)

	err := NewGlusterFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mount point")
}

func TestGlusterFSBackend_CreateDelete_RoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewGlusterFSBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	assert.DirExists(t, b.RootPath(teamID, volumeID))
	require.NoError(t, b.DeleteVolume(context.Background(), teamID, volumeID))
	assert.NoDirExists(t, b.RootPath(teamID, volumeID))
}

// ── SeaweedFS ────────────────────────────────────────────────────────────────

func TestSeaweedFSBackend_Type(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "seaweedfs", NewSeaweedFSBackend(t.TempDir()).Type())
}

func TestSeaweedFSBackend_Healthy_CorrectFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "fuse.seaweedfs")

	require.NoError(t, NewSeaweedFSBackend(root).Healthy(context.Background()))
}

func TestSeaweedFSBackend_Healthy_WrongFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "fuse.juicefs")

	err := NewSeaweedFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fuse.seaweedfs")
}

func TestSeaweedFSBackend_Healthy_NotMounted(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t)

	err := NewSeaweedFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mount point")
}

func TestSeaweedFSBackend_CreateDelete_RoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewSeaweedFSBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	require.NoError(t, b.DeleteVolume(context.Background(), teamID, volumeID))
}

// ── BeeGFS ───────────────────────────────────────────────────────────────────

func TestBeeGFSBackend_Type(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "beegfs", NewBeeGFSBackend(t.TempDir()).Type())
}

func TestBeeGFSBackend_Healthy_NativeKernelClient(t *testing.T) {
	// BeeGFS uses a native kernel module, fstype is "beegfs" (not fuse.*).
	root := t.TempDir()
	healthyMount(t, root, "beegfs")

	require.NoError(t, NewBeeGFSBackend(root).Healthy(context.Background()))
}

func TestBeeGFSBackend_Healthy_WrongFSType(t *testing.T) {
	root := t.TempDir()
	healthyMount(t, root, "fuse.beegfs") // FUSE variant would be wrong

	err := NewBeeGFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "beegfs")
}

func TestBeeGFSBackend_Healthy_NotMounted(t *testing.T) {
	root := t.TempDir()
	writeFakeMounts(t)

	err := NewBeeGFSBackend(root).Healthy(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a mount point")
}

func TestBeeGFSBackend_CreateDelete_RoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	teamID, volumeID := uuid.New(), uuid.New()
	b := NewBeeGFSBackend(root)

	require.NoError(t, b.CreateVolume(context.Background(), teamID, volumeID))
	require.NoError(t, b.DeleteVolume(context.Background(), teamID, volumeID))
}

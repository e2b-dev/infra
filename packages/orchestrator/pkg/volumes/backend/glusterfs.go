//go:build linux

package backend

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// GlusterFSBackend serves volumes from a GlusterFS volume mounted on the host.
//
// Deployment:
//
//	gluster volume create myvol replica 2 node1:/brick node2:/brick force
//	gluster volume start myvol
//	mount -t glusterfs node1:/myvol /mnt/glusterfs
//
// Config:
//
//	PERSISTENT_VOLUME_MOUNTS=default:glusterfs:/mnt/glusterfs
//
// GlusterFS is a peer-to-peer distributed NAS suitable for mixed workloads.
// It requires no central metadata server, which simplifies operations for
// teams that don't run Ceph.
type GlusterFSBackend struct {
	mp *MountpointBackend
}

func NewGlusterFSBackend(root string) *GlusterFSBackend {
	return &GlusterFSBackend{mp: NewMountpointBackend(root, "glusterfs")}
}

func (b *GlusterFSBackend) CreateVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.CreateVolume(ctx, teamID, volumeID)
}

func (b *GlusterFSBackend) DeleteVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.DeleteVolume(ctx, teamID, volumeID)
}

func (b *GlusterFSBackend) RootPath(teamID, volumeID uuid.UUID) string {
	return b.mp.RootPath(teamID, volumeID)
}

// Healthy verifies that the GlusterFS FUSE mount is live.
func (b *GlusterFSBackend) Healthy(ctx context.Context) error {
	if err := b.mp.Healthy(ctx); err != nil {
		return err
	}

	fsType, err := mountFSType(b.mp.root)
	if err != nil {
		return fmt.Errorf("glusterfs: reading fs type of %q: %w", b.mp.root, err)
	}

	if fsType != "fuse.glusterfs" {
		return fmt.Errorf("glusterfs: %q has fs type %q, expected fuse.glusterfs — is GlusterFS mounted?", b.mp.root, fsType)
	}

	return nil
}

func (b *GlusterFSBackend) Type() string { return "glusterfs" }

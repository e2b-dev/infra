//go:build linux

package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// CephFSBackend serves volumes from a CephFS filesystem mounted on the host.
//
// Deployment (kernel client):
//
//	mount -t ceph mon1,mon2:/ /mnt/cephfs -o name=admin,secret=<key>
//
// Deployment (FUSE client):
//
//	ceph-fuse /mnt/cephfs
//
// Config:
//
//	PERSISTENT_VOLUME_MOUNTS=default:cephfs:/mnt/cephfs
//
// CephFS delivers high aggregate throughput via the RADOS object store and is
// well-suited for large-file, high-throughput workloads (ML checkpoints, video
// assets).  The kernel client generally outperforms the FUSE client for
// sequential I/O.
type CephFSBackend struct {
	mp *MountpointBackend
}

func NewCephFSBackend(root string) *CephFSBackend {
	return &CephFSBackend{mp: NewMountpointBackend(root, "cephfs")}
}

func (b *CephFSBackend) CreateVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.CreateVolume(ctx, teamID, volumeID)
}

func (b *CephFSBackend) DeleteVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.DeleteVolume(ctx, teamID, volumeID)
}

func (b *CephFSBackend) RootPath(teamID, volumeID uuid.UUID) string {
	return b.mp.RootPath(teamID, volumeID)
}

// Healthy verifies that the CephFS mount is live.
// Accepts both the kernel client ("ceph") and FUSE client ("fuse.ceph-fuse")
// filesystem types.
func (b *CephFSBackend) Healthy(ctx context.Context) error {
	if err := b.mp.Healthy(ctx); err != nil {
		return err
	}

	fsType, err := mountFSType(b.mp.root)
	if err != nil {
		return fmt.Errorf("cephfs: reading fs type of %q: %w", b.mp.root, err)
	}

	// Kernel client reports "ceph"; FUSE client reports "fuse.ceph-fuse".
	if !strings.HasPrefix(fsType, "ceph") && fsType != "fuse.ceph-fuse" {
		return fmt.Errorf("cephfs: %q has fs type %q, expected ceph or fuse.ceph-fuse — is CephFS mounted?", b.mp.root, fsType)
	}

	return nil
}

func (b *CephFSBackend) Type() string { return "cephfs" }

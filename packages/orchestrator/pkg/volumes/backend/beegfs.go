//go:build linux

package backend

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// BeeGFSBackend serves volumes from a BeeGFS parallel filesystem mounted on
// the host.  BeeGFS (formerly FhGFS) is the go-to choice for HPC/AI workloads
// where aggregate throughput across many nodes matters more than fault
// tolerance.  Unlike the FUSE-based backends, BeeGFS uses a native kernel
// client module (beegfs), so its fstype in /proc/mounts is "beegfs".
//
// Deployment:
//
//	# Install BeeGFS client packages and configure /etc/beegfs/beegfs-client.conf
//	systemctl start beegfs-client
//	# Default mount point: /mnt/beegfs (configured in beegfs-mounts.conf)
//
// Config:
//
//	PERSISTENT_VOLUME_MOUNTS=default:beegfs:/mnt/beegfs
type BeeGFSBackend struct {
	mp *MountpointBackend
}

func NewBeeGFSBackend(root string) *BeeGFSBackend {
	return &BeeGFSBackend{mp: NewMountpointBackend(root, "beegfs")}
}

func (b *BeeGFSBackend) CreateVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.CreateVolume(ctx, teamID, volumeID)
}

func (b *BeeGFSBackend) DeleteVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.DeleteVolume(ctx, teamID, volumeID)
}

func (b *BeeGFSBackend) RootPath(teamID, volumeID uuid.UUID) string {
	return b.mp.RootPath(teamID, volumeID)
}

// Healthy verifies that the BeeGFS kernel client mount is live.
func (b *BeeGFSBackend) Healthy(ctx context.Context) error {
	if err := b.mp.Healthy(ctx); err != nil {
		return err
	}

	fsType, err := mountFSType(b.mp.root)
	if err != nil {
		return fmt.Errorf("beegfs: reading fs type of %q: %w", b.mp.root, err)
	}

	// BeeGFS native client shows as "beegfs" in /proc/mounts.
	if fsType != "beegfs" {
		return fmt.Errorf("beegfs: %q has fs type %q, expected beegfs — is the BeeGFS client mounted?", b.mp.root, fsType)
	}

	return nil
}

func (b *BeeGFSBackend) Type() string { return "beegfs" }

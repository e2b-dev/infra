//go:build linux

package backend

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// SeaweedFSBackend serves volumes from a SeaweedFS volume mounted via FUSE.
//
// Deployment:
//
//	# Start SeaweedFS master + volume server
//	weed master
//	weed volume -dir=/data/seaweedfs -max=5 -mserver=master:9333
//	# Mount via FUSE
//	weed mount -filer=filer:8888 -dir=/mnt/seaweedfs
//
// Config:
//
//	PERSISTENT_VOLUME_MOUNTS=default:seaweedfs:/mnt/seaweedfs
//
// SeaweedFS excels at storing large numbers of small files with low latency —
// it stores them in super-large volumes to avoid the POSIX metadata bottleneck
// that plagues ext4/xfs at scale.
type SeaweedFSBackend struct {
	mp *MountpointBackend
}

func NewSeaweedFSBackend(root string) *SeaweedFSBackend {
	return &SeaweedFSBackend{mp: NewMountpointBackend(root, "seaweedfs")}
}

func (b *SeaweedFSBackend) CreateVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.CreateVolume(ctx, teamID, volumeID)
}

func (b *SeaweedFSBackend) DeleteVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.DeleteVolume(ctx, teamID, volumeID)
}

func (b *SeaweedFSBackend) RootPath(teamID, volumeID uuid.UUID) string {
	return b.mp.RootPath(teamID, volumeID)
}

// Healthy verifies that the SeaweedFS FUSE mount is live.
func (b *SeaweedFSBackend) Healthy(ctx context.Context) error {
	if err := b.mp.Healthy(ctx); err != nil {
		return err
	}

	fsType, err := mountFSType(b.mp.root)
	if err != nil {
		return fmt.Errorf("seaweedfs: reading fs type of %q: %w", b.mp.root, err)
	}

	if fsType != "fuse.seaweedfs" {
		return fmt.Errorf("seaweedfs: %q has fs type %q, expected fuse.seaweedfs — is SeaweedFS FUSE mounted?", b.mp.root, fsType)
	}

	return nil
}

func (b *SeaweedFSBackend) Type() string { return "seaweedfs" }

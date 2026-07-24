//go:build linux

package backend

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// JuiceFSBackend serves volumes from a JuiceFS filesystem mounted on the host.
//
// Deployment:
//
//	juicefs format --storage s3 --bucket s3://bucket/prefix \
//	  --access-key KEY --secret-key SECRET redis://redis:6379/1 myvol
//	juicefs mount redis://redis:6379/1 /mnt/juicefs -d
//
// Config:
//
//	VOLUME_BACKEND_TYPE=juicefs
//	PERSISTENT_VOLUME_MOUNTS=default:/mnt/juicefs
type JuiceFSBackend struct {
	mp *MountpointBackend
}

func NewJuiceFSBackend(root string) *JuiceFSBackend {
	return &JuiceFSBackend{mp: NewMountpointBackend(root, "juicefs")}
}

func (b *JuiceFSBackend) CreateVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.CreateVolume(ctx, teamID, volumeID)
}

func (b *JuiceFSBackend) DeleteVolume(ctx context.Context, teamID, volumeID uuid.UUID) error {
	return b.mp.DeleteVolume(ctx, teamID, volumeID)
}

func (b *JuiceFSBackend) RootPath(teamID, volumeID uuid.UUID) string {
	return b.mp.RootPath(teamID, volumeID)
}

// Healthy verifies that the JuiceFS FUSE mount is live.
func (b *JuiceFSBackend) Healthy(ctx context.Context) error {
	if err := b.mp.Healthy(ctx); err != nil {
		return err
	}

	fsType, err := mountFSType(b.mp.root)
	if err != nil {
		return fmt.Errorf("juicefs: reading fs type of %q: %w", b.mp.root, err)
	}

	if fsType != "fuse.juicefs" {
		return fmt.Errorf("juicefs: %q has fs type %q, expected fuse.juicefs — is JuiceFS mounted?", b.mp.root, fsType)
	}

	return nil
}

func (b *JuiceFSBackend) Type() string { return "juicefs" }

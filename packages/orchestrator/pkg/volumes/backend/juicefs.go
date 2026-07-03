//go:build linux

package backend

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

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
//	PERSISTENT_VOLUME_MOUNTS=default:juicefs:/mnt/juicefs
//
// JuiceFS exposes a standard POSIX filesystem; all operations are directory
// manipulations on the mount point.  The metadata server (Redis / TiKV) gives
// ~1 ms stat latency — 10× better than NFS RPC for small-file workloads.
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

// Healthy checks that the JuiceFS mount is alive by confirming the kernel
// reports "fuse.juicefs" as the filesystem type in /proc/mounts.
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

// mountFSType returns the filesystem type for the given mountpoint from /proc/mounts.
func mountFSType(path string) (string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// /proc/mounts fields: device mountpoint fstype options dump pass
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[1] == path {
			return fields[2], nil
		}
	}

	return "", scanner.Err()
}

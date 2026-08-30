//go:build linux

package backend

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"
)

// Backend provisions and exposes a volume root path on the host filesystem.
// The NFS chroot layer mounts this path into sandboxes unchanged; the backend
// is responsible for ensuring the path exists, is writable, and is backed by
// an appropriate storage system.
type Backend interface {
	// CreateVolume provisions storage for the given volume.
	CreateVolume(ctx context.Context, teamID, volumeID uuid.UUID) error

	// DeleteVolume removes all data for the given volume.
	DeleteVolume(ctx context.Context, teamID, volumeID uuid.UUID) error

	// RootPath returns the absolute host-side path for the volume.
	// The caller may call this before CreateVolume to check paths; the
	// directory is only guaranteed to exist after CreateVolume returns.
	RootPath(teamID, volumeID uuid.UUID) string

	// Healthy verifies that the backend storage is accessible.
	// Called at startup and periodically; a non-nil error blocks sandbox mounts.
	Healthy(ctx context.Context) error

	// Type returns a human-readable backend type identifier for logging.
	Type() string
}

// volumePath builds the canonical team/volume subdirectory path under root.
func volumePath(root string, teamID, volumeID uuid.UUID) string {
	return filepath.Join(
		root,
		fmt.Sprintf("team-%s", teamID),
		fmt.Sprintf("vol-%s", volumeID),
	)
}

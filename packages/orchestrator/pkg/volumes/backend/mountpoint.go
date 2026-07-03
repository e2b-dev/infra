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

// procMountsPath is the path to the kernel mount table.
// Tests override this to inject a fake /proc/mounts without requiring root.
var procMountsPath = "/proc/mounts"

// MountpointBackend is the generic backend for any POSIX-compliant distributed
// filesystem that the operator has already mounted on the host.  It verifies
// that the configured root is an active mount point (not just a plain
// directory) so misconfiguration is caught at startup rather than at runtime.
type MountpointBackend struct {
	root       string
	backendTyp string
}

func NewMountpointBackend(root, typ string) *MountpointBackend {
	return &MountpointBackend{root: root, backendTyp: typ}
}

func (b *MountpointBackend) CreateVolume(_ context.Context, teamID, volumeID uuid.UUID) error {
	if err := os.MkdirAll(b.RootPath(teamID, volumeID), 0o700); err != nil {
		return fmt.Errorf("%s: create volume dir: %w", b.backendTyp, err)
	}

	return nil
}

func (b *MountpointBackend) DeleteVolume(_ context.Context, teamID, volumeID uuid.UUID) error {
	if err := os.RemoveAll(b.RootPath(teamID, volumeID)); err != nil {
		return fmt.Errorf("%s: delete volume dir: %w", b.backendTyp, err)
	}

	return nil
}

func (b *MountpointBackend) RootPath(teamID, volumeID uuid.UUID) string {
	return volumePath(b.root, teamID, volumeID)
}

// Healthy verifies that root exists and is an active mount point.
func (b *MountpointBackend) Healthy(_ context.Context) error {
	if _, err := os.Stat(b.root); err != nil {
		return fmt.Errorf("%s: root %q not accessible: %w", b.backendTyp, b.root, err)
	}

	mounted, err := isMountPoint(b.root)
	if err != nil {
		return fmt.Errorf("%s: checking mount status of %q: %w", b.backendTyp, b.root, err)
	}

	if !mounted {
		return fmt.Errorf("%s: %q is not a mount point — ensure the filesystem is mounted before starting the orchestrator", b.backendTyp, b.root)
	}

	return nil
}

func (b *MountpointBackend) Type() string { return b.backendTyp }

// isMountPoint reports whether path appears as a mount point in procMountsPath.
func isMountPoint(path string) (bool, error) {
	f, err := os.Open(procMountsPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[1] == path {
			return true, nil
		}
	}

	return false, scanner.Err()
}

// mountFSType returns the filesystem type for path from procMountsPath.
// Returns ("", nil) when no entry is found.
func mountFSType(path string) (string, error) {
	f, err := os.Open(procMountsPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// /proc/mounts: device mountpoint fstype options dump pass
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[1] == path {
			return fields[2], nil
		}
	}

	return "", scanner.Err()
}

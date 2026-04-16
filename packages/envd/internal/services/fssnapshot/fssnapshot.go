package fssnapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// UpperDev is the block device for the overlay upper layer (partition 2 / disk B).
	UpperDev = "/dev/vdb"

	upperMount   = "/mnt/upper"
	overlayMount = "/mnt/overlay"
	lowerMount   = "/mnt/lower"

	upperDataDir = "/mnt/upper/data"
	upperWorkDir = "/mnt/upper/work"
)

var pseudoFS = []string{"/proc", "/sys", "/dev", "/run"}

// SetupOverlay mounts the overlay upper device, creates the OverlayFS
// merge of the current rootfs (lower) with the upper device, and pivots
// into the overlay so all processes see a single writable filesystem.
//
// Call this after restoring a hidden base snapshot or on normal sandbox
// start. The lower rootfs (partition 1 / disk A) must already be mounted
// as the current root.
func SetupOverlay() error {
	for _, dir := range []string{upperMount, overlayMount, lowerMount, upperDataDir, upperWorkDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	if err := unix.Mount(UpperDev, upperMount, "ext4", unix.MS_NOATIME, ""); err != nil {
		return fmt.Errorf("mount %s on %s: %w", UpperDev, upperMount, err)
	}

	for _, dir := range []string{upperDataDir, upperWorkDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s on upper: %w", dir, err)
		}
	}

	overlayOpts := fmt.Sprintf("lowerdir=/,upperdir=%s,workdir=%s", upperDataDir, upperWorkDir)
	if err := unix.Mount("overlay", overlayMount, "overlay", 0, overlayOpts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}

	for _, pfs := range pseudoFS {
		dst := filepath.Join(overlayMount, pfs)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dst, err)
		}
		if err := unix.Mount(pfs, dst, "", unix.MS_MOVE, ""); err != nil {
			return fmt.Errorf("move %s → %s: %w", pfs, dst, err)
		}
	}

	if err := os.MkdirAll(filepath.Join(overlayMount, lowerMount), 0o755); err != nil {
		return fmt.Errorf("mkdir lower inside overlay: %w", err)
	}

	if err := unix.PivotRoot(overlayMount, filepath.Join(overlayMount, lowerMount)); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("chdir /: %w", err)
	}

	return nil
}

// TeardownOverlay reverses SetupOverlay: pivots back to the original
// rootfs (partition 1), moves pseudo-filesystems back, and unmounts the
// overlay and the upper device. Call this before FS-only pause so the
// host can safely persist only the upper device data.
//
// The caller should kill user processes and sync before calling this.
func TeardownOverlay() error {
	if err := unix.PivotRoot(lowerMount, filepath.Join(lowerMount, overlayMount)); err != nil {
		return fmt.Errorf("pivot_root back: %w", err)
	}

	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("chdir /: %w", err)
	}

	for _, pfs := range pseudoFS {
		src := filepath.Join(overlayMount, pfs)
		if err := unix.Mount(src, pfs, "", unix.MS_MOVE, ""); err != nil {
			return fmt.Errorf("move %s → %s: %w", src, pfs, err)
		}
	}

	if err := unix.Unmount(overlayMount, 0); err != nil {
		return fmt.Errorf("unmount overlay: %w", err)
	}

	if err := unix.Unmount(upperMount, 0); err != nil {
		return fmt.Errorf("unmount upper: %w", err)
	}

	return nil
}

// Sync flushes all pending filesystem writes to disk.
func Sync() {
	unix.Sync()
}

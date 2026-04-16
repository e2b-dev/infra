package fssnapshot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	blockDev = "/dev/vda"
	loopDev  = "/dev/loop0"

	upperMount   = "/mnt/upper"
	overlayMount = "/mnt/overlay"
	lowerMount   = "/mnt/lower"

	upperDataDir = "/mnt/upper/data"
	upperWorkDir = "/mnt/upper/work"
)

var pseudoFS = []string{"/proc", "/sys", "/dev", "/run"}

// SetupOverlay carves out the overlay region from the block device using
// losetup, mounts it as ext4, creates the OverlayFS merge with the current
// rootfs, and pivots into it.
//
// overlayOffsetBytes is the byte offset where the overlay ext4 starts within
// /dev/vda (= rootfs size). The orchestrator passes this in the request.
func SetupOverlay(overlayOffsetBytes int64) error {
	for _, dir := range []string{upperMount, overlayMount, lowerMount} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Create a loop device that exposes the overlay region of /dev/vda
	cmd := exec.Command("losetup",
		"--offset", fmt.Sprintf("%d", overlayOffsetBytes),
		loopDev, blockDev,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("losetup: %w (%s)", err, string(out))
	}

	if err := unix.Mount(loopDev, upperMount, "ext4", unix.MS_NOATIME, ""); err != nil {
		return fmt.Errorf("mount %s on %s: %w", loopDev, upperMount, err)
	}

	for _, dir := range []string{upperDataDir, upperWorkDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
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
// rootfs, unmounts the overlay, upper mount, and loop device.
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

	// Detach the loop device
	cmd := exec.Command("losetup", "-d", loopDev)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("losetup detach: %w (%s)", err, string(out))
	}

	return nil
}

// Sync flushes all pending filesystem writes to disk.
func Sync() {
	unix.Sync()
}

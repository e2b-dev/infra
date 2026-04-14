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
	TmpfsRoot = "/run/fs-snapshot-root"
	OldRoot   = "/oldroot"
	newRoot   = "/newroot"
	blockDev  = "/dev/vda"

	// Marker file written to tmpfs before switch-root.
	// If envd starts as PID 1 and this file exists, it enters hidden-base mode.
	MarkerFile = "/.hidden-base"
)

// IsHiddenBaseMode returns true if envd should run in hidden-base mode.
// This is detected by: (1) running as PID 1, and (2) the marker file exists,
// OR the -fs-mode=hidden-base flag was passed explicitly.
func IsHiddenBaseMode() bool {
	if os.Getpid() == 1 {
		if _, err := os.Stat(MarkerFile); err == nil {
			return true
		}
	}

	return false
}

// PrepareBase sets up a tmpfs root with the envd binary and invokes
// systemctl switch-root. After switch-root, systemd:
//  1. Stops all services (killing envd and everything else)
//  2. Pivots root to the tmpfs
//  3. Execs /sbin/init (our envd binary, symlinked) as PID 1
//
// The orchestrator must poll /health until the new envd is ready.
// The caller should flush the HTTP response before calling this, because
// systemd will SIGTERM this process shortly after.
func PrepareBase(envdBinaryPath string) error {
	if err := os.MkdirAll(TmpfsRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", TmpfsRoot, err)
	}

	if err := unix.Mount("tmpfs", TmpfsRoot, "tmpfs", 0, "size=64m"); err != nil {
		return fmt.Errorf("failed to mount tmpfs on %s: %w", TmpfsRoot, err)
	}

	subdirs := []string{"proc", "sys", "dev", "run", "tmp", "sbin", OldRoot, "newroot", "mnt"}
	for _, d := range subdirs {
		if err := os.MkdirAll(filepath.Join(TmpfsRoot, d), 0o755); err != nil {
			return fmt.Errorf("failed to create dir %s: %w", d, err)
		}
	}

	agentDst := filepath.Join(TmpfsRoot, "sbin", "init")
	if err := copyFile(envdBinaryPath, agentDst); err != nil {
		return fmt.Errorf("failed to copy envd binary: %w", err)
	}
	if err := os.Chmod(agentDst, 0o755); err != nil {
		return fmt.Errorf("failed to chmod: %w", err)
	}

	if err := os.WriteFile(filepath.Join(TmpfsRoot, MarkerFile), []byte("1"), 0o644); err != nil {
		return fmt.Errorf("failed to write marker: %w", err)
	}

	// systemctl switch-root tells systemd (PID 1) to:
	//   stop all services → pivot_root → exec /sbin/init
	// The INIT arg defaults to /sbin/init when omitted.
	cmd := exec.Command("systemctl", "switch-root", TmpfsRoot)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start switch-root: %w", err)
	}

	return nil
}

// UnmountOldRoot attempts to unmount the old ext4 root after switch-root.
// Tries a regular unmount first, then falls back to lazy unmount.
func UnmountOldRoot() error {
	path := OldRoot
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	if err := unix.Unmount(path, 0); err == nil {
		return nil
	}

	if err := unix.Unmount(path, unix.MNT_DETACH); err != nil {
		return fmt.Errorf("failed to lazy-unmount %s: %w", path, err)
	}

	return nil
}

// Sync flushes all pending filesystem writes to disk.
func Sync() {
	unix.Sync()
}

// MountAndPivot mounts the block device, moves pseudo-filesystems,
// and pivots into the new root. Used on FS-only resume after the
// hidden base snapshot is restored with NBD serving base + CoW diff.
//
// After this call, the process is in the ext4 rootfs. The old tmpfs
// is lazily unmounted (the running binary is still mapped from it but
// Go's static binary doesn't need re-reads).
func MountAndPivot() error {
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", newRoot, err)
	}

	if err := unix.Mount(blockDev, newRoot, "ext4", unix.MS_NOATIME, ""); err != nil {
		return fmt.Errorf("failed to mount %s on %s: %w", blockDev, newRoot, err)
	}

	pseudoFS := []struct{ src, dst string }{
		{"/proc", filepath.Join(newRoot, "proc")},
		{"/sys", filepath.Join(newRoot, "sys")},
		{"/dev", filepath.Join(newRoot, "dev")},
	}
	for _, pfs := range pseudoFS {
		if err := os.MkdirAll(pfs.dst, 0o755); err != nil {
			return fmt.Errorf("failed to create %s: %w", pfs.dst, err)
		}
		if err := unix.Mount(pfs.src, pfs.dst, "", unix.MS_MOVE, ""); err != nil {
			return fmt.Errorf("failed to move %s → %s: %w", pfs.src, pfs.dst, err)
		}
	}

	pivotOld := filepath.Join(newRoot, "mnt", "old")
	if err := os.MkdirAll(pivotOld, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", pivotOld, err)
	}

	if err := unix.PivotRoot(newRoot, pivotOld); err != nil {
		return fmt.Errorf("pivot_root failed: %w", err)
	}

	if err := syscall.Chdir("/"); err != nil {
		return fmt.Errorf("chdir / failed: %w", err)
	}

	// Lazy unmount the old tmpfs — safe because the static Go binary
	// is fully loaded in memory and doesn't re-read from disk.
	_ = unix.Unmount("/mnt/old", unix.MNT_DETACH)

	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, data, 0o755)
}

package fssnapshot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	tmpfsRoot = "/run/fs-snapshot-root"
	oldRoot   = "/oldroot"
	newRoot   = "/newroot"
	blockDev  = "/dev/vda"
)

// PrepareBase sets up a tmpfs root with the envd binary and calls
// systemctl switch-root. After switch-root, systemd kills all services,
// pivots to the tmpfs root, and execs envd in hidden-base mode as PID 1.
//
// This function responds to the HTTP caller, then systemd asynchronously
// tears down the old root. The orchestrator must poll /health until the
// new envd (hidden-base mode) is ready.
func PrepareBase(envdBinaryPath string, port int) error {
	dirs := []string{
		tmpfsRoot,
		filepath.Join(tmpfsRoot, "proc"),
		filepath.Join(tmpfsRoot, "sys"),
		filepath.Join(tmpfsRoot, "dev"),
		filepath.Join(tmpfsRoot, "run"),
		filepath.Join(tmpfsRoot, "tmp"),
		filepath.Join(tmpfsRoot, "oldroot"),
		filepath.Join(tmpfsRoot, "newroot"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("failed to create dir %s: %w", d, err)
		}
	}

	if err := unix.Mount("tmpfs", tmpfsRoot, "tmpfs", 0, "size=64m"); err != nil {
		return fmt.Errorf("failed to mount tmpfs on %s: %w", tmpfsRoot, err)
	}

	for _, d := range dirs[1:] {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("failed to create dir %s on tmpfs: %w", d, err)
		}
	}

	agentDst := filepath.Join(tmpfsRoot, "envd")
	if err := copyFile(envdBinaryPath, agentDst); err != nil {
		return fmt.Errorf("failed to copy envd binary: %w", err)
	}
	if err := os.Chmod(agentDst, 0o755); err != nil {
		return fmt.Errorf("failed to chmod envd binary: %w", err)
	}

	cmd := exec.Command(
		"systemctl", "switch-root",
		tmpfsRoot,
		"/envd",
		fmt.Sprintf("-fs-mode=hidden-base"),
		fmt.Sprintf("-port=%d", port),
	)
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
	err := unix.Unmount(oldRoot, 0)
	if err == nil {
		return nil
	}

	err = unix.Unmount(oldRoot, unix.MNT_DETACH)
	if err != nil {
		return fmt.Errorf("failed to lazy-unmount %s: %w", oldRoot, err)
	}

	return nil
}

// MountAndPivot mounts the block device, moves pseudo-filesystems,
// and pivots into the new root. Used on FS-only resume after the
// hidden base snapshot is restored with NBD serving base + CoW diff.
func MountAndPivot() error {
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", newRoot, err)
	}

	if err := unix.Mount(blockDev, newRoot, "ext4", 0, ""); err != nil {
		return fmt.Errorf("failed to mount %s on %s: %w", blockDev, newRoot, err)
	}

	pseudoFS := []struct {
		src string
		dst string
	}{
		{"/proc", filepath.Join(newRoot, "proc")},
		{"/sys", filepath.Join(newRoot, "sys")},
		{"/dev", filepath.Join(newRoot, "dev")},
	}

	for _, pfs := range pseudoFS {
		if err := os.MkdirAll(pfs.dst, 0o755); err != nil {
			return fmt.Errorf("failed to create %s: %w", pfs.dst, err)
		}
		if err := unix.Mount(pfs.src, pfs.dst, "", unix.MS_MOVE, ""); err != nil {
			return fmt.Errorf("failed to move %s to %s: %w", pfs.src, pfs.dst, err)
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

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = unix.Unmount("/mnt/old", unix.MNT_DETACH)
	}()

	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, data, 0o755)
}

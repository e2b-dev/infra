package api

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// nfsMounter runs the mount operations; tests replace it.
type nfsMounter interface {
	Mount(ctx context.Context, nfsTarget, path string) error
	// MakeDir creates path and its parents; ctx bounds it even when NFS stops answering.
	MakeDir(ctx context.Context, path string) error
	// Unmount detaches whatever is mounted at path; nothing mounted is not an error.
	Unmount(path string) error
	// MountID returns the ID of the mount that path resolves to.
	MountID(path string) (uint64, error)
}

type execNFSMounter struct{}

// maxMountOutputBytes caps the mount output kept in an error.
const maxMountOutputBytes = 512

// nfsCmdWaitDelay bounds how long a canceled command may keep its output pipes open
// before Wait stops reading them.
const nfsCmdWaitDelay = 2 * time.Second

func (execNFSMounter) Mount(ctx context.Context, nfsTarget, path string) error {
	args := []string{"-v", "-t", "nfs", "-o", "fg,hard," + nfsOptions, nfsTarget, path}

	if output, err := runProcessGroup(ctx, "mount", args...); err != nil {
		return fmt.Errorf("`mount %s` failed: %w: %s", strings.Join(args, " "), err, output)
	}

	return nil
}

func (execNFSMounter) MakeDir(ctx context.Context, path string) error {
	if output, err := runProcessGroup(ctx, "mkdir", "-p", path); err != nil {
		return fmt.Errorf("`mkdir -p %s` failed: %w: %s", path, err, output)
	}

	return nil
}

// runProcessGroup runs a command in its own process group and kills the whole group
// when ctx ends. mount forks mount.nfs, which would outlive a kill of mount alone and
// could finish the mount after the timeout. It returns the combined output, trimmed and
// capped at maxMountOutputBytes.
func runProcessGroup(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}

		return nil
	}
	cmd.WaitDelay = nfsCmdWaitDelay

	data, err := cmd.CombinedOutput()

	return strings.TrimSpace(string(data[:min(len(data), maxMountOutputBytes)])), err
}

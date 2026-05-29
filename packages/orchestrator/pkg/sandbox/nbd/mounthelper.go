//go:build linux

package nbd

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/testutils"
)

// MountNBDDevice mounts the supplied nbd device path as ext4 at mountPath and
// returns a Cleaner that unmounts it. Intended for the mount-build-rootfs
// debug utility and package tests.
func MountNBDDevice(device DevicePath, mountPath string) (*testutils.Cleaner, error) {
	var cleaner testutils.Cleaner

	err := unix.Mount(device, mountPath, "ext4", 0, "")
	if err != nil {
		return &cleaner, fmt.Errorf("failed to mount device to mount path: %w", err)
	}

	cleaner.Add(func(cleanupCtx context.Context) error {
		ticker := time.NewTicker(600 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-cleanupCtx.Done():
				fmt.Fprintf(os.Stderr, "failed to unmount device from mount path in time\n")

				return nil
			case <-ticker.C:
				err = unix.Unmount(mountPath, 0)
				if err == nil {
					return nil
				}

				fmt.Fprintf(os.Stderr, "failed to unmount device from mount path: %v\n", err)
			}
		}
	})

	return &cleaner, nil
}

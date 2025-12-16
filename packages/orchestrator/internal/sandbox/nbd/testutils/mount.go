package testutils

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
)

func MountNBDDevice(device nbd.DevicePath, mountPath string) (*Cleaner, error) {
	var cleaner Cleaner

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

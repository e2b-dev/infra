//go:build linux

package fsfreeze

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// FIFREEZE / FITHAW ioctl request codes — _IOWR('X', 119/120, int). They are not
// exported by x/sys/unix. The values are stable on the architectures envd runs
// on (x86_64, arm64), which use the generic ioctl encoding. FIFREEZE/FITHAW
// ignore the ioctl argument, so passing 0 is safe.
const (
	fiFreeze = 0xC0045877
	fiThaw   = 0xC0045878
)

type linuxFreezer struct{}

// New returns a Freezer backed by the FIFREEZE/FITHAW ioctls.
func New() Freezer {
	return linuxFreezer{}
}

func (linuxFreezer) Freeze(mountpoint string) error {
	f, err := os.Open(mountpoint)
	if err != nil {
		return fmt.Errorf("open %s: %w", mountpoint, err)
	}
	defer f.Close()

	if err := unix.IoctlSetInt(int(f.Fd()), fiFreeze, 0); err != nil {
		// EBUSY means the filesystem is already frozen; treat as success so the
		// call is idempotent.
		if errors.Is(err, unix.EBUSY) {
			return nil
		}

		return fmt.Errorf("FIFREEZE %s: %w", mountpoint, err)
	}

	return nil
}

func (linuxFreezer) Thaw(mountpoint string) error {
	f, err := os.Open(mountpoint)
	if err != nil {
		return fmt.Errorf("open %s: %w", mountpoint, err)
	}
	defer f.Close()

	if err := unix.IoctlSetInt(int(f.Fd()), fiThaw, 0); err != nil {
		// EINVAL means the filesystem is not frozen; treat as success so the
		// call is idempotent.
		if errors.Is(err, unix.EINVAL) {
			return nil
		}

		return fmt.Errorf("FITHAW %s: %w", mountpoint, err)
	}

	return nil
}

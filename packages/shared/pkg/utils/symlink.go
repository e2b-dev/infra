package utils

import (
	"fmt"
	"os"
)

func SymlinkForce(oldname, newname string) error {
	err := os.Remove(newname)
	if err != nil {
		return fmt.Errorf("error removing rootfs symlink: %w", err)
	}

	return os.Symlink(oldname, newname)
}

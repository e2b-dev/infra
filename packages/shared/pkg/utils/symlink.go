package utils

import (
	"fmt"
	"os"
	"path/filepath"
)

func SymlinkForce(oldname, newname string) error {
	// Ignore error if the symlink does not exist
	_ = os.Remove(newname)

	return os.Symlink(oldname, newname)
}

func AbsSymlink(oldname, newname string) error {
	var err error

	if !filepath.IsAbs(oldname) {
		oldname, err = filepath.Abs(oldname)
		if err != nil {
			return fmt.Errorf("failed to get absolute path of old path: %w", err)
		}
	}

	if !filepath.IsAbs(newname) {
		newname, err = filepath.Abs(newname)
		if err != nil {
			return fmt.Errorf("failed to get absolute path of new path: %w", err)
		}
	}

	return os.Symlink(oldname, newname)
}

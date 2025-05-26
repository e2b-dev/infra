package utils

import (
	"os"
)

func SymlinkForce(oldname, newname string) error {
	// Ignore error if the symlink does not exist
	_ = os.Remove(newname)
	return os.Symlink(oldname, newname)
}

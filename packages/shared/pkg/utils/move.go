package utils

import (
	"fmt"
	"os"
)

// AtomicMove tries to rename a file but will not replace the target if it already exists.
// If the file already exists, the file will be deleted.
func AtomicMove(oldPath, newPath string) error {
	return atomicMove(oldPath, newPath, &osFns{})
}

func atomicMove(oldPath, newPath string, os fileOps) error {
	if err := os.Link(oldPath, newPath); err != nil {
		return fmt.Errorf("failed to create hard link: %w", err)
	}

	if err := os.Remove(oldPath); err != nil {
		return fmt.Errorf("failed to remove existing file: %w", err)
	}

	return nil
}

type osFns struct{}

func (o osFns) Link(oldPath, newPath string) error {
	return os.Link(oldPath, newPath)
}

func (o osFns) Remove(path string) error {
	return os.Remove(path)
}

var _ fileOps = (*osFns)(nil)

type fileOps interface {
	Link(oldPath, newPath string) error
	Remove(path string) error
}

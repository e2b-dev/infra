package ioutils

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrAtomicWriteCommitted = errors.New("atomic write committed")

func WriteToFileFromReader(path string, r io.Reader) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	if _, err = io.Copy(f, r); err != nil {
		return err
	}

	return f.Sync()
}

// WriteToFileFromReaderAtomically replaces an existing regular file without
// exposing partial contents. Errors wrapping ErrAtomicWriteCommitted mean the
// replacement is visible, but parent-directory durability could not be confirmed.
func WriteToFileFromReaderAtomically(path string, r io.Reader) (err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("atomic write target %q is not a regular file", path)
	}

	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".atomic-write-*")
	if err != nil {
		return err
	}
	tempPath := f.Name()
	closed := false
	committed := false
	defer func() {
		if !closed {
			if closeErr := f.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close temporary file: %w", closeErr))
			}
		}
		if !committed {
			if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove temporary file: %w", removeErr))
			}
		}
	}()

	if _, err = io.Copy(f, r); err != nil {
		return err
	}
	if err = f.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	err = f.Close()
	closed = true
	if err != nil {
		return err
	}

	if err = os.Rename(tempPath, path); err != nil {
		return err
	}
	committed = true

	if err = syncDirectory(dir); err != nil {
		return fmt.Errorf("%w: sync parent directory: %w", ErrAtomicWriteCommitted, err)
	}

	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}

	return errors.Join(dir.Sync(), dir.Close())
}

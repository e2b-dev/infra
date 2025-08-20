package ioutils

import (
	"io"
	"os"
)

func WriteToFileFromReader(path string, r io.Reader) (err error) {
	// Create (truncate if exists) with 0644 perms
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	// Make sure we return a close error if that's the only error.
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = cerr
		}
	}()

	if _, err = io.Copy(f, r); err != nil {
		return err
	}
	return f.Sync() // ensure contents hit disk
}

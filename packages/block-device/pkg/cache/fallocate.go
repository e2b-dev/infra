package cache

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func fallocate(size int64, out *os.File) error {
	err := unix.Fallocate(int(out.Fd()), unix.FALLOC_FL_KEEP_SIZE, 0, size)
	if err != nil {
		return fmt.Errorf("error allocating file: %w", err)
	}

	return nil
}

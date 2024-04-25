package cache

import (
	"os"

	"golang.org/x/sys/unix"
)

func fallocate(size int64, out *os.File) error {
	return unix.Fallocate(int(out.Fd()), unix.FALLOC_FL_KEEP_SIZE, 0, size)
}

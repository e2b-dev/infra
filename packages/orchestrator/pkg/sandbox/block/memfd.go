package block

import (
	"syscall"

	"golang.org/x/sys/unix"
)

type Memfd struct {
	memfd int
}

func NewFromFd(fd int) *Memfd {
	syscall.CloseOnExec(fd)

	return &Memfd{
		memfd: fd,
	}
}

func (m *Memfd) FreePages(offset, size int64) error {
	return unix.Fallocate(m.memfd, unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, offset, size)
}

func (m *Memfd) Close() error {
	return syscall.Close(m.memfd)
}

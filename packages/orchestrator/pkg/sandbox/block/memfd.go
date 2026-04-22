package block

import (
	"syscall"
)

type Memfd struct {
	memfd int
}

func NewFromFd(fd int) *Memfd {
	return &Memfd{
		memfd: fd,
	}
}

func (m *Memfd) Close() error {
	return syscall.Close(m.memfd)
}

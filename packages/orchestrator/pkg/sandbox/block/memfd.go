package block

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

type Memfd struct {
	memfd int
	mmap  []byte
}

func NewFromFd(fd, totalSize int) (*Memfd, error) {
	syscall.CloseOnExec(fd)

	data, err := syscall.Mmap(fd, 0, totalSize, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("failed to mmap memfd: %w", errors.Join(err, syscall.Close(fd)))
	}

	return &Memfd{
		memfd: fd,
		mmap:  data,
	}, nil
}

func (m *Memfd) Slice(offset, size int64) []byte {
	return m.mmap[offset : offset+size]
}

func (m *Memfd) FreePages(offset, size int64) error {
	return unix.Fallocate(m.memfd, unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, offset, size)
}

func (m *Memfd) Close() error {
	return errors.Join(syscall.Munmap(m.mmap), syscall.Close(m.memfd))
}

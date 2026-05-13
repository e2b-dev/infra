//go:build linux

package block

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
)

// Memfd wraps a memfd received from Firecracker
type Memfd struct {
	fd   int
	size int

	mmapOnce sync.Once
	mmap     []byte
	mmapErr  error
}

// NewFromFd creates a new Memfd wrapper of a memfd object (fd) that
// backs memory of size bytes big
func NewFromFd(fd, size int) *Memfd {
	return &Memfd{
		fd:   fd,
		size: size,
	}
}

// ensureMapped lazily mmaps the whole memfd. Safe to call from multiple
// goroutines; the mapping is performed exactly once.
func (m *Memfd) ensureMapped() error {
	m.mmapOnce.Do(func() {
		mm, err := syscall.Mmap(m.fd, 0, m.size, syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			m.mmapErr = fmt.Errorf("failed to mmap memfd: %w", err)

			return
		}
		m.mmap = mm
	})

	return m.mmapErr
}

// Slice returns a zero-copy view of [offset, offset+size) of the memfd.
// The returned slice is valid until Close is called.
func (m *Memfd) Slice(offset, size int64) ([]byte, error) {
	if err := m.ensureMapped(); err != nil {
		return nil, err
	}
	if offset < 0 || offset >= int64(m.size) || offset+size > int64(m.size) {
		return nil, fmt.Errorf("range [%d, %d) out of bounds (size %d)", offset, offset+size, m.size)
	}

	return m.mmap[offset : offset+size], nil
}

// Close unmaps memory if it was previously mmap'ed and closes the memfd file descriptor
// if not already closed.
func (m *Memfd) Close() error {
	var err error

	if m.mmap != nil {
		if e := syscall.Munmap(m.mmap); e != nil {
			err = fmt.Errorf("munmap memfd: %w", e)
		}
		m.mmap = nil
	}

	if m.fd >= 0 {
		if e := syscall.Close(m.fd); e != nil {
			err = errors.Join(err, fmt.Errorf("close memfd: %w", e))
		}
		m.fd = -1
	}

	return err
}

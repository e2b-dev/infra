package memory

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

var (
	_ io.ReaderAt = (*View)(nil)
	_ io.Closer   = (*View)(nil)
)

// MemoryNotFaultedError is returned on read when the page was not faulted in (syscall.EIO error)
type MemoryNotFaultedError struct {
	addr    uintptr
	written int
	err     error
}

func (e MemoryNotFaultedError) Error() string {
	return fmt.Sprintf("memory not faulted: %v (written %d): %v", e.addr, e.written, e.err)
}

func (e MemoryNotFaultedError) Unwrap() error {
	return e.err
}

// View exposes memory of the underlying process via offsets, remapped via the mapping to the host virtual address space.
type View struct {
	m       *Mapping
	procMem *os.File
	fd      int
}

func NewView(pid int, m *Mapping) (*View, error) {
	fd, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open memory file: %w", err)
	}

	return &View{
		procMem: fd,
		fd:      int(fd.Fd()),
		m:       m,
	}, nil
}

// ReadAt reads data from the memory view at the given offset.
// If this operation crosses a page boundary, it will read the data from the next page.
//
// If you try to read missing pages that are not yet faulted in via UFFD, this will return an error.
func (v *View) ReadAt(d []byte, off int64) (n int, err error) {
	for n < len(d) {
		addr, size, err := v.m.GetHostVirtAddr(off + int64(n))
		if err != nil {
			return n, fmt.Errorf("failed to get host virt addr: %w", err)
		}

		remainingSize := min(size, int64(len(d)-n))

		written, err := syscall.Pread(v.fd, d[n:n+int(remainingSize)], int64(addr))
		if errors.Is(err, syscall.EIO) {
			return n, MemoryNotFaultedError{addr: addr, written: written, err: err}
		}

		if err != nil {
			return n, fmt.Errorf("failed to read from /proc/%d/mem: %w", v.procMem.Name(), err)
		}

		n += written
	}

	return n, nil
}

func (v *View) Close() error {
	return v.procMem.Close()
}

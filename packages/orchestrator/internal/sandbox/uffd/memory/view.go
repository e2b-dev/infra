package memory

import (
	"fmt"
	"io"
	"os"
)

// Check for readAt interface implementation
var _ io.ReaderAt = (*View)(nil)

// View exposes memory of the underlying process, with the mappings applied.
type View struct {
	m  MemoryMap
	fd *os.File
}

func NewView(pid int, m MemoryMap) (*View, error) {
	fd, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open memory file: %w", err)
	}

	return &View{
		fd: fd,
		m:  m,
	}, nil
}

func (m *View) Close() error {
	return m.fd.Close()
}

func (m *View) ReadAt(data []byte, off int64) (int, error) {
	offset, _, err := m.m.GetHostVirtAddr(off)
	if err != nil {
		return 0, fmt.Errorf("failed to get host virt addr: %w", err)
	}

	return m.fd.ReadAt(data, offset)
}

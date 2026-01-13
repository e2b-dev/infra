package block

import (
	"fmt"
	"io"
	"os"
)

// MemoryMapper translates guest memory offsets to host virtual address ranges.
type MemoryMapper interface {
	GetHostVirtRanges(off int64, size int64) ([]Range, error)
}

// ProcessMemoryReader reads from a process's memory using /proc/<pid>/mem
// with offset translation via memory mapping.
type ProcessMemoryReader struct {
	fd *os.File
	m  MemoryMapper
}

// Ensure ProcessMemoryReader implements io.ReaderAt
var _ io.ReaderAt = (*ProcessMemoryReader)(nil)

// NewProcessMemoryReader creates a reader for process memory.
func NewProcessMemoryReader(pid int, m MemoryMapper) (*ProcessMemoryReader, error) {
	fd, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open process memory: %w", err)
	}

	return &ProcessMemoryReader{
		fd: fd,
		m:  m,
	}, nil
}

// ReadAt reads len(p) bytes from the process memory at the given offset.
// The offset is translated using the memory mapping to the host virtual address.
func (r *ProcessMemoryReader) ReadAt(p []byte, off int64) (int, error) {
	// Get host virtual address ranges for the requested offset
	ranges, err := r.m.GetHostVirtRanges(off, int64(len(p)))
	if err != nil {
		return 0, fmt.Errorf("failed to get host virt ranges: %w", err)
	}

	var totalRead int
	for _, rng := range ranges {
		n, err := r.fd.ReadAt(p[totalRead:totalRead+int(rng.Size)], rng.Start)
		totalRead += n
		if err != nil {
			return totalRead, err
		}
	}

	return totalRead, nil
}

// Close closes the process memory file descriptor.
func (r *ProcessMemoryReader) Close() error {
	return r.fd.Close()
}

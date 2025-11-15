package memory

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"syscall"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

var (
	_ io.ReaderAt = (*Memory)(nil)
	_ io.Closer   = (*Memory)(nil)
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

// Memory exposes memory of the underlying process via offsets, remapped via the mapping to the host virtual address space.
type Memory struct {
	m           *Mapping
	procMem     *os.File
	procMemFd   int
	procPagemap *os.File
	pid         int
}

func NewMemory(pid int, m *Mapping) (*Memory, error) {
	procMem, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open memory file: %w", err)
	}

	procPagemap, err := os.Open(fmt.Sprintf("/proc/%d/pagemap", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open pagemap: %w", err)
	}

	return &Memory{
		procMem:     procMem,
		procMemFd:   int(procMem.Fd()),
		procPagemap: procPagemap,
		m:           m,
		pid:         pid,
	}, nil
}

// ReadAt reads data from the memory view at the given offset.
// If this operation crosses a page boundary, it will read the data from the next page.
//
// If you try to read missing pages that are not yet faulted in via UFFD, this will return an error.
func (v *Memory) ReadAt(d []byte, off int64) (n int, err error) {
	for n < len(d) {
		addr, size, err := v.m.GetHostVirtAddr(off + int64(n))
		if err != nil {
			return 0, fmt.Errorf("failed to get host virt addr: %w", err)
		}

		remainingSize := min(size, int64(len(d)-n))

		written, err := syscall.Pread(v.procMemFd, d[n:n+int(remainingSize)], int64(addr))
		if errors.Is(err, syscall.EIO) {
			return n, MemoryNotFaultedError{addr: addr, written: written, err: err}
		}

		if err != nil {
			return n, fmt.Errorf("failed to read from : %w", err)
		}

		n += written
	}

	return n, nil
}

func (v *Memory) Close() error {
	return errors.Join(v.procMem.Close(), v.procPagemap.Close())
}

func (v *Memory) pages() iter.Seq2[uintptr, int64] {
	return func(yield func(uintptr, int64) bool) {
		for _, r := range v.m.Regions {
			for addr, off := range r.pages() {
				if !yield(addr, off) {
					break
				}
			}
		}
	}
}

func (v *Memory) ResetSoftDirty() error {
	return os.WriteFile(fmt.Sprintf("/proc/%d/clear_refs", v.pid), []byte("4"), 0666)
}

func (v *Memory) SoftDirty() (*block.Tracker, error) {
	dirty := block.NewTracker(v.m.PageSize())

	buf := make([]byte, 8)

	for addr, off := range v.pages() {

		// This will always be normal page size as the page is still tracked in 4k pages.
		pagemapOffset := int64((addr >> 12) * 8)

		// Read the pagemap entry
		n, err := v.procPagemap.ReadAt(buf, pagemapOffset)
		if err != nil || n != 8 {
			return nil, fmt.Errorf("failed to read pagemap at VA 0x%x offset %d: %w",
				addr, pagemapOffset, err)
		}

		entry := binary.LittleEndian.Uint64(buf)

		// Bit 55 = soft dirty
		softDirty := (entry >> 55) & 1
		// Optional: require Present=1
		// present := (entry >> 63) & 1
		present := (entry >> 63) & 1
		// What are other bits here

		if softDirty == 1 && present == 1 {
			dirty.Add(off)
		}
	}

	return dirty, nil
}

// TODO: One page dirty flip by WP mode=0

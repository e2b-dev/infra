package memory

import (
	"fmt"
	"io"
	"os"
)

var _ io.ReaderAt = (*View)(nil)
var _ io.Closer = (*View)(nil)

// View exposes memory of the underlying process, with the mappings applied.
type View struct {
	m       *Mapping
	procMem *os.File
}

func NewView(pid int, m *Mapping) (*View, error) {
	fd, err := os.Open(fmt.Sprintf("/proc/%d/mem", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to open memory file: %w", err)
	}

	return &View{
		procMem: fd,
		m:       m,
	}, nil
}

// ReadAt reads data from the memory view at the given offset.
// If this operation crosses a page boundary, it will read the data from the next page.
//
// If you try to read missing pages that are not yet faulted in via UFFD, this will return an error.
func (v *View) ReadAt(d []byte, off int64) (n int, err error) {
	ranges, err := v.m.GetHostVirtRanges(off, int64(len(d)))
	if err != nil {
		return 0, fmt.Errorf("failed to get host virt regions: %w", err)
	}

	for _, r := range ranges {
		written, err := v.procMem.ReadAt(d[n:r.Size], r.Start)
		if err != nil {
			return 0, fmt.Errorf("failed to read at: %w", err)
		}

		n += written
	}

	return n, nil
}

func (v *View) Close() error {
	return v.procMem.Close()
}

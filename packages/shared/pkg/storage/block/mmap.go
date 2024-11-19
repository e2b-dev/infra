package block

import (
	"errors"
	"fmt"
	"os"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/unix"
)

type MmapCache struct {
	marker    *Marker
	filePath  string
	size      int64
	blockSize int64
	mmap      mmap.MMap
}

// Ensure that you only write at specific offsets once and only read from these offsets after writing.
// Use external mutex if you need to ensure this.
func NewMmapCache(size, blockSize int64, filePath string) (*MmapCache, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	defer f.Close()

	// This should create a sparse file on Linux.
	err = f.Truncate(size)
	if err != nil {
		return nil, fmt.Errorf("error allocating file: %w", err)
	}

	mm, err := mmap.MapRegion(f, int(size), unix.PROT_READ|unix.PROT_WRITE|unix.PROT_EXEC, mmap.RDWR|mmap.EXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("error mapping file: %w", err)
	}

	blocks := (size + blockSize - 1) / blockSize

	return &MmapCache{
		mmap:      mm,
		filePath:  filePath,
		size:      size,
		marker:    NewMarker(uint(blocks)),
		blockSize: blockSize,
	}, nil
}

func (m *MmapCache) ReadAt(b []byte, off int64) (int, error) {
	if !m.isCached(off, int64(len(b))) {
		return 0, ErrBytesNotAvailable{}
	}

	end := off + int64(len(b))
	if end > m.size {
		end = m.size
	}

	return copy(b, m.mmap[off:end]), nil
}

func (m *MmapCache) WriteAt(b []byte, off int64) (int, error) {
	end := off + int64(len(b))
	if end > m.size {
		end = m.size
	}

	n := copy(m.mmap[off:end], b)

	m.mark(off, int64(n))

	return n, nil
}

func (m *MmapCache) Close() error {
	mmapErr := m.mmap.Unmap()

	removeErr := os.RemoveAll(m.filePath)

	return errors.Join(mmapErr, removeErr)
}

func (m *MmapCache) Sync() error {
	err := m.mmap.Flush()
	if err != nil {
		return fmt.Errorf("error flushing mmap: %w", err)
	}

	return nil
}

func (m *MmapCache) Size() (int64, error) {
	return m.size, nil
}

// Slice returns a slice of the mmap.
// This cache is returned only if the data is already present in the cache.
func (m *MmapCache) Slice(off, length int64) ([]byte, error) {
	if !m.isCached(off, length) {
		return nil, ErrBytesNotAvailable{}
	}

	end := off + length
	if end > m.size {
		end = m.size
	}

	return m.mmap[off:end], nil
}

func (m *MmapCache) BlockSize() int64 {
	return m.blockSize
}

func (m *MmapCache) isCached(off, length int64) bool {
	for i := off; i < off+length; i += m.blockSize {
		if !m.marker.IsMarked(i / m.blockSize) {
			return false
		}
	}

	return true
}

func (m *MmapCache) mark(off, length int64) {
	for i := off; i < off+length; i += m.blockSize {
		m.marker.Mark(i / m.blockSize)
	}
}

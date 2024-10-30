package cache

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/unix"
)

type MmapCache struct {
	marker    *block.Marker
	filePath  string
	size      int64
	blockSize int64
	mmap      mmap.MMap
	mu        sync.RWMutex
}

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

	return &MmapCache{
		mmap:      mm,
		filePath:  filePath,
		size:      size,
		marker:    block.NewMarker(uint(size / blockSize)),
		blockSize: blockSize,
	}, nil
}

func (m *MmapCache) ReadAt(b []byte, off int64) (int, error) {
	if !m.IsMarked(off, int64(len(b))) {
		return 0, block.ErrBytesNotAvailable{}
	}

	end := off + int64(len(b))
	if end > m.size {
		end = m.size
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return copy(b, m.mmap[off:end]), nil
}

func (m *MmapCache) WriteAt(b []byte, off int64) (int, error) {
	end := off + int64(len(b))
	if end > m.size {
		end = m.size
	}

	m.mu.Lock()
	n := copy(m.mmap[off:end], b)
	m.mu.Unlock()

	m.Mark(off, int64(n))

	return n, nil
}

func (m *MmapCache) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mmapErr := m.mmap.Unmap()

	removeErr := os.Remove(m.filePath)

	return errors.Join(mmapErr, removeErr)
}

func (m *MmapCache) Sync() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.mmap.Flush()
	if err != nil {
		return fmt.Errorf("error flushing mmap: %w", err)
	}

	return nil
}

func (m *MmapCache) Size() (int64, error) {
	return m.size, nil
}

func (m *MmapCache) ReadRaw(off, length int64) ([]byte, func(), error) {
	if !m.IsMarked(off, length) {
		return nil, nil, block.ErrBytesNotAvailable{}
	}

	end := off + length
	if end > m.size {
		end = m.size
	}

	m.mu.RLock()

	return m.mmap[off:end], func() {
		m.mu.RUnlock()
	}, nil
}

func (m *MmapCache) BlockSize() int64 {
	return m.blockSize
}

func (m *MmapCache) IsMarked(off, length int64) bool {
	for i := off; i < off+length; i += m.blockSize {
		if !m.marker.IsMarked(i / m.blockSize) {
			return false
		}
	}

	return true
}

func (m *MmapCache) Mark(off, length int64) {
	for i := off; i < off+length; i += m.blockSize {
		m.marker.Mark(i / m.blockSize)
	}
}

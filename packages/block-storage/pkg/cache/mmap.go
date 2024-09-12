package cache

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/edsrzf/mmap-go"
)

type MmapCache struct {
	size      int64
	blockSize int64
	filePath  string
	marker    *block.Marker
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

	mm, err := mmap.Map(f, mmap.COPY, 0)
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
	if !m.marker.IsMarked(off / m.blockSize) {
		return 0, block.ErrBytesNotAvailable{}
	}

	length := int64(len(b))
	if length+off > m.size {
		length = m.size - off
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return copy(b, m.mmap[off:off+length]), nil
}

func (m *MmapCache) WriteAt(b []byte, off int64) (int, error) {
	length := int64(len(b))
	if length+off > m.size {
		length = m.size - off
	}

	m.mu.Lock()
	n := copy(m.mmap[off:off+length], b)
	m.mu.Unlock()

	for i := off; i < off+int64(n); i += m.blockSize {
		m.marker.Mark(i / m.blockSize)
	}

	return n, nil
}

func (m *MmapCache) Close() error {
	mmapErr := m.mmap.Unmap()

	removeErr := os.Remove(m.filePath)

	return errors.Join(mmapErr, removeErr)
}

func (m *MmapCache) Sync() error {
	return m.mmap.Flush()
}

func (m *MmapCache) Size() int64 {
	return m.size
}

func (m *MmapCache) ReadRaw(off, length int64) ([]byte, func(), error) {
	if !m.marker.IsMarked(off / m.blockSize) {
		return nil, nil, block.ErrBytesNotAvailable{}
	}

	if length+off > m.size {
		length = m.size - off
	}

	m.mu.RLock()

	return m.mmap[off : off+length], func() {
		m.mu.RUnlock()
	}, nil
}

func (m *MmapCache) BlockSize() int64 {
	return m.blockSize
}

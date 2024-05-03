package cache

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/edsrzf/mmap-go"
)

type mmapedFile struct {
	file *os.File
	mmap mmap.MMap
	mu   sync.RWMutex
	size int64
}

func newMmappedFile(size int64, filePath string) (*mmapedFile, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	err = f.Truncate(size)
	if err != nil {
		return nil, fmt.Errorf("error allocating file: %w", err)
	}

	mm, err := mmap.Map(f, mmap.RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("error mapping file: %w", err)
	}

	return &mmapedFile{
		mmap: mm,
		file: f,
		size: int64(len(mm)),
	}, nil
}

func (m *mmapedFile) ReadAt(b []byte, off int64) (int, error) {
	length := int64(len(b))
	if length+off > m.size {
		length = m.size - off
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return copy(b, m.mmap[off:off+length]), nil
}

func (m *mmapedFile) WriteAt(b []byte, off int64) (int, error) {
	length := int64(len(b))
	if length+off > m.size {
		length = m.size - off
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return copy(m.mmap[off:off+length], b), nil
}

func (m *mmapedFile) Close() error {
	flushErr := m.mmap.Flush()

	mmapErr := m.mmap.Unmap()
	closeErr := m.file.Close()

	return errors.Join(flushErr, mmapErr, closeErr)
}

func (m *mmapedFile) Sync() error {
	return m.mmap.Flush()
}

func (m *mmapedFile) Size() int64 {
	return m.size
}

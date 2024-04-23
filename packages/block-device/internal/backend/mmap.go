package backend

import (
	"errors"
	"os"
	"sync"

	"github.com/edsrzf/mmap-go"
)

type mmapped struct {
	mmap mmap.MMap
	file *os.File
	mu   sync.RWMutex
}

func newMmapped(size int64, filePath string) (*mmapped, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Truncate or expand the file to ensure it's the right size.
	// Is should be sparse.
	if err = f.Truncate(size); err != nil {
		return nil, err
	}

	// TODO: Try to preallocate the file via fallocate.
	// err = fallocate.preAllocate(size, f)
	// if err != nil {
	// 	return nil, err
	// }

	// Memory-map the file
	mm, err := mmap.Map(f, mmap.RDWR, 0)
	if err != nil {
		return nil, err
	}

	return &mmapped{
		mmap: mm,
		file: f,
	}, nil
}

func (m *mmapped) ReadAt(b []byte, off int64) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return copy(b, m.mmap[off:off+int64(len(b))]), nil
}

func (m *mmapped) WriteAt(b []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return copy(m.mmap[off:off+int64(len(b))], b), nil
}

func (m *mmapped) Close() error {
	flushErr := m.mmap.Flush()

	mmapErr := m.mmap.Unmap()
	closeErr := m.file.Close()

	return errors.Join(flushErr, mmapErr, closeErr)
}

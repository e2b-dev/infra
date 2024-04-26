package cache

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/edsrzf/mmap-go"
)

type mmapedFile struct {
	File *os.File
	mmap mmap.MMap
	mu   sync.RWMutex
}

func newMmappedFile(size int64, filePath string, createFile bool) (*mmapedFile, error) {
	var flag int

	if createFile {
		flag = os.O_RDWR | os.O_CREATE
	} else {
		flag = os.O_RDWR
	}

	f, err := os.OpenFile(filePath, flag, 0o666)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	if createFile {
		err = fallocate(size, f)
		if err != nil {
			return nil, fmt.Errorf("error allocating file: %w", err)
		}
	}

	mm, err := mmap.Map(f, mmap.RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("error mapping file: %w", err)
	}

	return &mmapedFile{
		mmap: mm,
		File: f,
	}, nil
}

func (m *mmapedFile) ReadAt(b []byte, off int64) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return copy(b, m.mmap[off:off+int64(len(b))]), nil
}

func (m *mmapedFile) WriteAt(b []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return copy(m.mmap[off:off+int64(len(b))], b), nil
}

func (m *mmapedFile) Close() error {
	flushErr := m.mmap.Flush()

	mmapErr := m.mmap.Unmap()
	closeErr := m.File.Close()

	return errors.Join(flushErr, mmapErr, closeErr)
}

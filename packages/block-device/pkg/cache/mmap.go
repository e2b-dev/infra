package cache

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/unix"
)

type mmapedFile struct {
	File *os.File
	mmap mmap.MMap
	mu   sync.RWMutex
	size int64
}

func newMmappedFile(size int64, filePath string, createFile bool, overlay bool) (*mmapedFile, error) {
	var flag int

	if createFile {
		flag = os.O_RDWR | os.O_CREATE
	} else {
		flag = os.O_RDWR
	}

	f, err := os.OpenFile(filePath, flag, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	if createFile {
		err = f.Truncate(size)
		if err != nil {
			return nil, fmt.Errorf("error allocating file: %w", err)
		}
	}

	mmapFlags := unix.MAP_SHARED
	if overlay {
		// TODO: Test overlay mmaped file — if the private is only process wise we would need to run separate processes for handlng each fc rootfs overlay though.
		// TODO: Check mmap.COPY - multiple map initializations for the same file?
		mmapFlags = unix.MAP_PRIVATE
	}

	mm, err := mmap.Map(f, mmap.RDWR, mmapFlags)
	if err != nil {
		return nil, fmt.Errorf("error mapping file: %w", err)
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("error getting file size: %w", err)
	}

	return &mmapedFile{
		mmap: mm,
		File: f,
		size: stat.Size(),
	}, nil
}

func (m *mmapedFile) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 || off > m.size-1 {
		return 0, fmt.Errorf("invalid offset: %d", off)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return copy(b, m.mmap[off:off+int64(len(b))]), nil
}

func (m *mmapedFile) WriteAt(b []byte, off int64) (int, error) {
	if off < 0 || off > m.size-1 {
		return 0, fmt.Errorf("invalid offset: %d", off)
	}

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

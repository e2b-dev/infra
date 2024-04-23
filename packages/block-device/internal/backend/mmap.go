package backend

import (
	"errors"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/block-device/internal/block"
	"github.com/edsrzf/mmap-go"
)

type mmapped struct {
	mmap          mmap.MMap
	file          *os.File
	mu            sync.RWMutex
	SparseTracker *block.SparseFile
}

func newMmapped(size int64, filePath string, createFile bool) (*mmapped, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return nil, err
	}

	var sparseTracker *block.SparseFile
	if createFile {
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
	} else {
		sparseTracker = block.NewSparseFileChecker(f)
	}

	// Memory-map the file
	mm, err := mmap.Map(f, mmap.RDWR, 0)
	if err != nil {
		return nil, err
	}

	return &mmapped{
		mmap:          mm,
		file:          f,
		SparseTracker: sparseTracker,
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

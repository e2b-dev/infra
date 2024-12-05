package block

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/unix"
)

type cache struct {
	filePath  string
	size      int64
	blockSize int64
	mmap      mmap.MMap
	mu        sync.RWMutex
	dirty     sync.Map
}

func newCache(size, blockSize int64, filePath string) (*cache, error) {
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

	return &cache{
		mmap:      mm,
		filePath:  filePath,
		size:      size,
		blockSize: blockSize,
	}, nil
}

func (m *cache) ReadAt(b []byte, off int64) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	slice, err := m.Slice(off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("error slicing mmap: %w", err)
	}

	return copy(b, slice), nil
}

func (m *cache) WriteAt(b []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	end := off + int64(len(b))
	if end > m.size {
		end = m.size
	}

	n := copy(m.mmap[off:end], b)

	m.setIsCached(off, end-off)

	return n, nil
}

func (m *cache) Close() error {
	return errors.Join(
		m.mmap.Unmap(),
		os.RemoveAll(m.filePath),
	)
}

func (m *cache) Size() (int64, error) {
	return m.size, nil
}

// Slice returns a slice of the mmap.
// When using WriteAt you must ensure thread safety, ideally by only writing to the same block once and the exposing the slice.
func (m *cache) Slice(off, length int64) ([]byte, error) {
	if m.isCached(off, length) {
		end := off + length
		if end > m.size {
			end = m.size
		}

		return m.mmap[off:end], nil
	}

	return nil, ErrBytesNotAvailable{}
}

func (m *cache) isCached(off, length int64) bool {
	for _, block := range listBlocks(off, off+length, m.blockSize) {
		_, dirty := m.dirty.Load(block.start)
		if !dirty {
			return false
		}
	}

	return true
}

func (m *cache) setIsCached(off, length int64) {
	for _, block := range listBlocks(off, off+length, m.blockSize) {
		m.dirty.Store(block.start, struct{}{})
	}
}
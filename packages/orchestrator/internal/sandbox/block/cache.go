package block

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/bits-and-blooms/bitset"
	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Cache struct {
	filePath  string
	size      int64
	blockSize int64
	mmap      *mmap.MMap
	mu        sync.RWMutex
	dirty     sync.Map
	dirtyFile bool
}

// When we are passing filePath that is a file that has content we want to server want to use dirtyFile = true.
func NewCache(size, blockSize int64, filePath string, dirtyFile bool) (*Cache, error) {
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

	return &Cache{
		mmap:      &mm,
		filePath:  filePath,
		size:      size,
		blockSize: blockSize,
		dirtyFile: dirtyFile,
	}, nil
}

func (m *Cache) Export(out io.Writer) (*bitset.BitSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.mmap.Flush()
	if err != nil {
		return nil, fmt.Errorf("error flushing mmap: %w", err)
	}

	tracked := bitset.New(uint(header.TotalBlocks(m.size, m.blockSize)))

	m.dirty.Range(func(key, value any) bool {
		block := header.BlockIdx(key.(int64), m.blockSize)

		tracked.Set(uint(block))

		_, err := out.Write((*m.mmap)[key.(int64) : key.(int64)+m.blockSize])
		if err != nil {
			fmt.Printf("error writing to out: %v\n", err)

			return false
		}

		return true
	})

	return tracked, nil
}

func (m *Cache) ReadAt(b []byte, off int64) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	slice, err := m.Slice(off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("error slicing mmap: %w", err)
	}

	return copy(b, slice), nil
}

func (m *Cache) WriteAt(b []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.WriteAtWithoutLock(b, off)
}

func (m *Cache) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return errors.Join(
		m.mmap.Unmap(),
		os.RemoveAll(m.filePath),
	)
}

func (m *Cache) Size() (int64, error) {
	return m.size, nil
}

// Slice returns a slice of the mmap.
// When using Slice you must ensure thread safety, ideally by only writing to the same block once and the exposing the slice.
func (m *Cache) Slice(off, length int64) ([]byte, error) {
	if m.dirtyFile || m.isCached(off, length) {
		end := off + length
		if end > m.size {
			end = m.size
		}

		return (*m.mmap)[off:end], nil
	}

	return nil, ErrBytesNotAvailable{}
}

func (m *Cache) isCached(off, length int64) bool {
	for _, blockOff := range header.BlocksOffsets(length, m.blockSize) {
		_, dirty := m.dirty.Load(off + blockOff)
		if !dirty {
			return false
		}
	}

	return true
}

func (m *Cache) setIsCached(off, length int64) {
	for _, blockOff := range header.BlocksOffsets(length, m.blockSize) {
		m.dirty.Store(off+blockOff, struct{}{})
	}
}

// When using WriteAtWithoutLock you must ensure thread safety, ideally by only writing to the same block once and the exposing the slice.
func (m *Cache) WriteAtWithoutLock(b []byte, off int64) (int, error) {
	end := off + int64(len(b))
	if end > m.size {
		end = m.size
	}

	n := copy((*m.mmap)[off:end], b)

	m.setIsCached(off, end-off)

	return n, nil
}

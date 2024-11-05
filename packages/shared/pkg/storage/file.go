package storage

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/bits-and-blooms/bitset"
)

type ErrBytesNotAvailable struct{}

func (ErrBytesNotAvailable) Error() string {
	return "The requested bytes are not available on the device"
}

type FileCache struct {
	file      *os.File
	marker    *bitset.BitSet
	lock      sync.RWMutex
	blockSize int64
	size      int64
}

func NewFileCache(size, blockSize int64, cachePath string) (*FileCache, error) {
	file, err := os.Create(cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache file: %w", err)
	}

	err = file.Truncate(size)
	if err != nil {
		closeErr := file.Close()
		removeErr := os.Remove(cachePath)

		return nil, fmt.Errorf("failed to truncate cache file: %w", errors.Join(closeErr, removeErr, err))
	}

	// Need to round up, because the last block might not be full.
	blocks := (size + blockSize - 1) / blockSize

	return &FileCache{
		file:      file,
		marker:    bitset.New(uint(blocks)),
		blockSize: blockSize,
		size:      size,
	}, nil
}

func (b *FileCache) ReadAt(p []byte, off int64) (n int, err error) {
	b.lock.RLock()
	defer b.lock.RUnlock()

	if !b.marker.Test(uint(off / b.blockSize)) {
		return 0, ErrBytesNotAvailable{}
	}

	n, err = b.file.ReadAt(p, off)

	return
}

func (b *FileCache) WriteAt(p []byte, off int64) (n int, err error) {
	b.lock.Lock()
	defer b.lock.Unlock()

	n, err = b.file.WriteAt(p, off)

	b.marker.Set(uint(off / b.blockSize))

	return
}

func (b *FileCache) Size() (int64, error) {
	stat, err := b.file.Stat()
	if err != nil {
		return -1, err
	}

	return stat.Size(), nil
}

func (b *FileCache) Sync() error {
	return b.file.Sync()
}

func (b *FileCache) Close() error {
	closeErr := b.file.Close()

	removeErr := os.Remove(b.file.Name())

	return errors.Join(closeErr, removeErr)
}

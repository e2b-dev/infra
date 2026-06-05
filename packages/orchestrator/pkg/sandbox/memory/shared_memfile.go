//go:build linux

// Package memory provides shared memory management for layered VM snapshots.
// The SharedMemfileManager enables multiple Firecracker microVMs to share
// read-only memory pages via the host page cache, using MAP_PRIVATE to
// let the kernel handle CoW isolation automatically.
package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// SharedMemfileManager manages memfiles that can be shared across multiple VMs.
// It uses MAP_PRIVATE so that the kernel page cache naturally deduplicates
// read-only pages while CoW isolates writes per VM.
type SharedMemfileManager struct {
	mu       sync.RWMutex
	memfiles map[string]*SharedMemfile // keyed by absolute file path
}

// SharedMemfile represents a memory-mapped file that can be shared
// across multiple Firecracker VM processes. Read-only pages hit the
// host page cache; writes trigger CoW per process.
type SharedMemfile struct {
	path   string
	f      *os.File
	Data   []byte // mmap(MAP_PRIVATE) result — writable for CoW support
	Size   int64
	refCnt atomic.Int32
}

// NewSharedMemfileManager creates a new shared memfile manager.
func NewSharedMemfileManager() *SharedMemfileManager {
	return &SharedMemfileManager{
		memfiles: make(map[string]*SharedMemfile),
	}
}

// Map returns a shared memory mapping for the given file path.
// Multiple callers mapping the same path share physical pages until
// a write triggers CoW. Safe for concurrent use.
func (m *SharedMemfileManager) Map(path string) (*SharedMemfile, error) {
	// Fast path: read lock for existing entry.
	// We must hold the read lock while incrementing refCnt to prevent a
	// TOCTOU race: between RUnlock and refCnt.Add(1), another goroutine
	// could call Unmap(), decrement refCnt to 0, munmap the data, and
	// delete the entry. Holding RLock blocks Unmap()'s write lock.
	m.mu.RLock()
	existing, ok := m.memfiles[path]
	if ok {
		existing.refCnt.Add(1)
	}
	m.mu.RUnlock()
	if ok {
		return existing, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if existing, ok = m.memfiles[path]; ok {
		existing.refCnt.Add(1)
		return existing, nil
	}

	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open memfile %s: %w", path, err)
	}

	stat, err := os.Stat(path)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat memfile %s: %w", path, err)
	}
	size := stat.Size()

	if size == 0 {
		f.Close()
		return nil, fmt.Errorf("memfile %s is empty", path)
	}

	// MAP_PRIVATE: writes trigger CoW, read-only pages are shared in host page cache.
	// MAP_POPULATE: pre-fault pages to reduce first-access minor faults.
	data, err := unix.Mmap(int(f.Fd()), 0, int(size),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_PRIVATE|unix.MAP_POPULATE)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap memfile %s: %w", path, err)
	}

	// MADV_SEQUENTIAL reduces kernel page cache lock contention when
	// multiple VMs concurrently map the same large shared memfile.
	// It tells the LRU reclaimer to prefer sequential access order,
	// reducing lock bouncing on the mmap_sem and pagecache radix tree.
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)

	sf := &SharedMemfile{
		path: path,
		f:    f,
		Data: data,
		Size: size,
	}
	sf.refCnt.Store(1)
	m.memfiles[path] = sf

	sharedMemoryBytes.Add(context.Background(), size)
	sharedMemfileMapCount.Add(context.Background(), 1)

	return sf, nil
}

// Unmap decrements the reference count and releases resources when
// the last reference is dropped.
func (m *SharedMemfileManager) Unmap(path string) error {
	m.mu.Lock()
	sf, ok := m.memfiles[path]
	if !ok {
		m.mu.Unlock()
		return nil
	}

	if refs := sf.refCnt.Add(-1); refs > 0 {
		m.mu.Unlock()
		return nil
	}

	// Last reference — release resources.
	delete(m.memfiles, path)
	m.mu.Unlock()

	var errs []error

	if err := unix.Munmap(sf.Data); err != nil {
		errs = append(errs, fmt.Errorf("munmap: %w", err))
	}
	if err := sf.f.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close fd: %w", err))
	}

	sharedMemoryBytes.Add(context.Background(), -sf.Size)
	sharedMemfileMapCount.Add(context.Background(), -1)

	return errors.Join(errs...)
}

// GetStats returns the current count and total bytes of tracked shared memfiles.
func (m *SharedMemfileManager) GetStats() (count int, totalBytes int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sf := range m.memfiles {
		count++
		totalBytes += sf.Size
	}
	return count, totalBytes
}

// Close unmaps all tracked memfiles and releases all resources.
func (m *SharedMemfileManager) Close() error {
	m.mu.Lock()
	paths := make([]string, 0, len(m.memfiles))
	for path := range m.memfiles {
		paths = append(paths, path)
	}
	m.mu.Unlock()

	var errs []error
	for _, path := range paths {
		if err := m.Unmap(path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

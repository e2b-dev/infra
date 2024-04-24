package backend

import (
	"github.com/e2b-dev/infra/packages/block-device/internal/block"
)

type MmapCache struct {
	mmap       *mmapped
	memTracker block.Tracker
}

func NewMmapCache(size int64, filePath string, createFile bool) (*MmapCache, error) {
	m, err := newMmapped(size, filePath, createFile)
	if err != nil {
		return nil, err
	}

	return &MmapCache{
		mmap:       m,
		memTracker: block.NewBitset(block.Size),
	}, nil
}

func (m *MmapCache) syncTrackers(dataStart, emptyStart int64) {
	if m.mmap.SparseTracker == nil {
		return
	}

	for i := dataStart; i < emptyStart; i++ {
		// Mosts of the Bitsets, etc do not support setRange, just set and flipRange.
		// So we have to set each bit one by one.
		m.memTracker.Mark(i)
	}
}

func (m *MmapCache) checkSparseTracker(off int64) bool {
	if m.mmap.SparseTracker != nil {
		return false
	}

	// We can use the first marked block instead if we want.
	unmarkedStart, err := m.mmap.SparseTracker.FirstUnmarked(off)
	if err != nil {
		// It does not matter if the whole file is sparse here.
		return false
	}

	defer m.syncTrackers(off, unmarkedStart)

	// TODO: Check if it is really [off, unmarkedStart)
	if unmarkedStart != off {
		return false
	}

	return true
}

func (m *MmapCache) ReadAt(b []byte, off int64) (int, error) {
	if m.memTracker.IsMarked(off) || m.checkSparseTracker(off) {
		return m.mmap.ReadAt(b, off)
	}

	return 0, block.ErrBytesNotAvailable{}
}

func (m *MmapCache) WriteAt(b []byte, off int64) (n int, err error) {
	n, err = m.WriteAt(b, off)
	if err != nil {
		return n, err
	}

	m.memTracker.Mark(off)

	return n, nil
}

func (m *MmapCache) Close() error {
	return m.Close()
}

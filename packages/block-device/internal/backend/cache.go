package backend

import (
	"github.com/e2b-dev/infra/packages/block-device/internal/block"
)

type MmapCache struct {
	mmap    *mmapped
	tracker block.Tracker
}

func NewMMapCache(size int64, filePath string) (*MmapCache, error) {
	m, err := newMmapped(size, filePath)
	if err != nil {
		return nil, err
	}

	return &MmapCache{
		mmap:    m,
		tracker: block.NewBitset(block.Size),
	}, nil
}

func (m *MmapCache) ReadAt(b []byte, off int64) (int, error) {
	if m.tracker.IsMarked(off) {
		return m.mmap.ReadAt(b, off)
	}

	return 0, block.ErrBytesNotAvailable{}
}

func (m *MmapCache) WriteAt(b []byte, off int64) (n int, err error) {
	n, err = m.WriteAt(b, off)
	if err != nil {
		return n, err
	}

	m.tracker.Mark(off)

	return n, nil
}

func (m *MmapCache) Close() error {
	return m.Close()
}

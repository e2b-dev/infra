package cache

import (
	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
)

type MmapCache struct {
	mmap   *mmapedFile
	marker *block.Marker
}

func NewMmapCache(size int64, filePath string) (*MmapCache, error) {
	m, err := newMmappedFile(size, filePath)
	if err != nil {
		return nil, err
	}

	return &MmapCache{
		mmap:   m,
		marker: block.NewMarker(uint(size / block.Size)),
	}, nil
}

func (m *MmapCache) ReadAt(b []byte, off int64) (int, error) {
	if m.IsMarked(off) {
		return m.mmap.ReadAt(b, off)
	}

	return 0, block.ErrBytesNotAvailable{}
}

// WriteAt can write more than one block at a time.
func (m *MmapCache) WriteAt(b []byte, off int64) (n int, err error) {
	n, err = m.mmap.WriteAt(b, off)
	if err != nil {
		return n, err
	}

	for i := off; i < off+int64(n); i += block.Size {
		m.marker.Mark(i / block.Size)
	}

	return n, nil
}

func (m *MmapCache) Close() error {
	return m.mmap.Close()
}

func (m *MmapCache) IsMarked(off int64) bool {
	return m.marker.IsMarked(off / block.Size)
}

func (m *MmapCache) Sync() error {
	return m.mmap.Sync()
}

func (m *MmapCache) Size() int64 {
	return m.mmap.Size()
}

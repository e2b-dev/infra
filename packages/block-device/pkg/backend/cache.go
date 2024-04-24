package backend

import (
	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
)

type MmapCache struct {
	mmap   *mmapped
	marker block.Marker
}

func NewMmapCache(size int64, filePath string, createFile bool) (*MmapCache, error) {
	m, err := newMmapped(size, filePath, createFile)
	if err != nil {
		return nil, err
	}

	return &MmapCache{
		mmap:   m,
		marker: block.NewBitset(),
	}, nil
}

func (m *MmapCache) syncMarkers(dataStart, emptyStart int64) {
	if m.mmap.marked == nil {
		return
	}

	start := dataStart / block.Size
	end := (emptyStart + block.Size - 1) / block.Size // Use +block.Size-1 to ensure the last block is included if emptyStart is not a multiple of block.Size

	for blockIdx := start; blockIdx < end; blockIdx++ {
		m.marker.Mark(blockIdx)
	}
}

func (m *MmapCache) checkSparseMarker(off int64) bool {
	if m.mmap.marked != nil {
		return false
	}

	// We can use the first marked block instead if we want.
	unmarkedStart, err := m.mmap.marked.FirstUnmarked(off)
	if err != nil {
		// It does not matter if the whole file is sparse here.
		return false
	}

	defer m.syncMarkers(off, unmarkedStart)

	// TODO: Check if it is really [off, unmarkedStart)
	return unmarkedStart != off
}

func (m *MmapCache) ReadAt(b []byte, off int64) (int, error) {
	if m.marker.IsMarked(off/block.Size) || m.checkSparseMarker(off) {
		return m.mmap.ReadAt(b, off)
	}

	return 0, block.ErrBytesNotAvailable{}
}

func (m *MmapCache) WriteAt(b []byte, off int64) (n int, err error) {
	n, err = m.mmap.WriteAt(b, off)
	if err != nil {
		return n, err
	}

	m.marker.Mark(off / block.Size)

	return n, nil
}

func (m *MmapCache) Close() error {
	return m.Close()
}
